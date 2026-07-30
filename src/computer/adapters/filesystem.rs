use std::path::{Path, PathBuf};

use async_trait::async_trait;
use sha2::{Digest, Sha256};
use tokio::io::AsyncWriteExt;
use uuid::Uuid;

use crate::{
    computer::application::{ApplicationError, LocalAgent, LocalAgentState, ports::AgentHomePort},
    ids::AgentId,
};

pub(in crate::computer) struct AgentHomeAdapter {
    computer_home: PathBuf,
    codex_config_source: Option<PathBuf>,
    codex_auth_source: Option<PathBuf>,
}

impl AgentHomeAdapter {
    pub(in crate::computer) fn new(
        computer_home: PathBuf,
        codex_config_source: Option<PathBuf>,
        codex_auth_source: Option<PathBuf>,
    ) -> Self {
        Self {
            computer_home,
            codex_config_source,
            codex_auth_source,
        }
    }

    async fn write_profile(&self, agent: &LocalAgent) -> Result<(), ApplicationError> {
        let home = self.agent_home(agent);
        create_private_dir(&home).await?;
        for relative in [
            "memory",
            "workspace",
            "drivers/codex",
            "drivers/builtin",
            "sessions",
            "runs",
            "logs",
        ] {
            create_private_dir(&home.join(relative)).await?;
        }
        if agent.driver == crate::computer::core::session::DriverKind::Codex {
            self.install_codex_sources(&home.join("drivers/codex"))
                .await?;
        }
        let profile = serde_json::to_vec(agent).map_err(|_| ApplicationError::Internal)?;
        let profile_path = home.join("profile.json");
        let temporary = home.join(format!("profile.{}.tmp", Uuid::now_v7()));
        let mut options = tokio::fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            options.mode(0o600);
        }
        let mut file = options
            .open(&temporary)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        file.write_all(&profile)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        file.sync_all()
            .await
            .map_err(|_| ApplicationError::Internal)?;
        drop(file);
        tokio::fs::rename(&temporary, &profile_path)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        restrict_file(&profile_path).await?;
        Ok(())
    }

    async fn install_codex_sources(&self, codex_home: &Path) -> Result<(), ApplicationError> {
        if let Some(source) = &self.codex_config_source {
            let encoded = tokio::fs::read_to_string(source)
                .await
                .map_err(|_| ApplicationError::DriverUnavailable)?;
            let input: toml::Table =
                toml::from_str(&encoded).map_err(|_| ApplicationError::DriverUnavailable)?;
            let sanitized = sanitize_codex_config(&input)?;
            let encoded =
                toml::to_string_pretty(&sanitized).map_err(|_| ApplicationError::Internal)?;
            write_private_file(&codex_home.join("config.toml"), encoded.as_bytes()).await?;
        }
        if let Some(source) = &self.codex_auth_source {
            let encoded = tokio::fs::read(source)
                .await
                .map_err(|_| ApplicationError::DriverUnavailable)?;
            serde_json::from_slice::<serde_json::Value>(&encoded)
                .map_err(|_| ApplicationError::DriverUnavailable)?;
            write_private_file(&codex_home.join("auth.json"), &encoded).await?;
        }
        Ok(())
    }

    async fn fingerprint(&self, agent_id: AgentId) -> Result<String, ApplicationError> {
        let workspace = self.agent_home_for_id(agent_id).join("workspace");
        let canonical = tokio::fs::canonicalize(&workspace)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        let metadata = tokio::fs::metadata(&canonical)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        let mut digest = Sha256::new();
        digest.update(canonical.as_os_str().as_encoded_bytes());
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            digest.update(metadata.dev().to_le_bytes());
            digest.update(metadata.ino().to_le_bytes());
        }
        #[cfg(not(unix))]
        digest.update(metadata.len().to_le_bytes());
        Ok(hex::encode(digest.finalize()))
    }

    pub(in crate::computer) fn agent_home(&self, agent: &LocalAgent) -> PathBuf {
        self.agent_home_for_id(agent.agent_id)
    }

    pub(in crate::computer) async fn read_attachment_source(
        &self,
        agent_id: AgentId,
        path: &Path,
    ) -> Result<(String, Vec<u8>), ApplicationError> {
        validate_attachment_path(path)?;
        let home = tokio::fs::canonicalize(self.agent_home_for_id(agent_id))
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        let target = tokio::fs::canonicalize(home.join(path))
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        if !target.starts_with(&home) {
            return Err(ApplicationError::Conflict);
        }
        let metadata = tokio::fs::symlink_metadata(&target)
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        if !metadata.file_type().is_file() || metadata.file_type().is_symlink() {
            return Err(ApplicationError::Conflict);
        }
        let name = target
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or(ApplicationError::Conflict)?
            .to_owned();
        let content = tokio::fs::read(target)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        Ok((name, content))
    }

    pub(in crate::computer) async fn write_attachment_output(
        &self,
        agent_id: AgentId,
        path: &Path,
        content: &[u8],
    ) -> Result<(), ApplicationError> {
        validate_attachment_path(path)?;
        let home = tokio::fs::canonicalize(self.agent_home_for_id(agent_id))
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        let relative_parent = path.parent().unwrap_or_else(|| Path::new(""));
        let requested_parent = home.join(relative_parent);
        create_private_dir(&requested_parent).await?;
        let parent = tokio::fs::canonicalize(&requested_parent)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        if !parent.starts_with(&home) {
            return Err(ApplicationError::Conflict);
        }
        let target = parent.join(path.file_name().ok_or(ApplicationError::Conflict)?);
        write_new_private_file(&target, content).await
    }

    fn agent_home_for_id(&self, agent_id: AgentId) -> PathBuf {
        self.computer_home.join("agents").join(agent_id.to_string())
    }
}

fn validate_attachment_path(path: &Path) -> Result<(), ApplicationError> {
    if path.as_os_str().is_empty()
        || path.is_absolute()
        || path
            .components()
            .any(|component| !matches!(component, std::path::Component::Normal(_)))
        || !matches!(
            path.components().next(),
            Some(std::path::Component::Normal(root))
                if matches!(root.to_str(), Some("workspace" | "memory" | "runs"))
        )
    {
        return Err(ApplicationError::Conflict);
    }
    Ok(())
}

#[async_trait]
impl AgentHomePort for AgentHomeAdapter {
    async fn agent(&mut self, agent_id: AgentId) -> Result<LocalAgent, ApplicationError> {
        let bytes = tokio::fs::read(self.agent_home_for_id(agent_id).join("profile.json"))
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        let agent: LocalAgent =
            serde_json::from_slice(&bytes).map_err(|_| ApplicationError::Internal)?;
        if agent.agent_id != agent_id {
            return Err(ApplicationError::Internal);
        }
        Ok(agent)
    }

    async fn provision(&mut self, agent: LocalAgent) -> Result<(), ApplicationError> {
        self.write_profile(&agent).await
    }

    async fn configure(&mut self, mut agent: LocalAgent) -> Result<(), ApplicationError> {
        agent.state = self.agent(agent.agent_id).await?.state;
        self.write_profile(&agent).await
    }

    async fn suspend(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        let mut agent = self.agent(agent_id).await?;
        agent.state = LocalAgentState::Suspended;
        self.write_profile(&agent).await
    }

    async fn retire(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        let target = self.agent_home_for_id(agent_id);
        let agents_root = self.computer_home.join("agents");
        if target.parent() != Some(agents_root.as_path()) || target == self.computer_home {
            return Err(ApplicationError::Conflict);
        }
        match tokio::fs::remove_dir_all(target).await {
            Ok(()) => Ok(()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(_) => Err(ApplicationError::Internal),
        }
    }

    async fn workspace_fingerprint(
        &mut self,
        agent_id: AgentId,
    ) -> Result<String, ApplicationError> {
        self.fingerprint(agent_id).await
    }

    async fn read_memory(
        &mut self,
        agent_id: AgentId,
        path: &Path,
    ) -> Result<Vec<u8>, ApplicationError> {
        let memory_root = self.agent_home_for_id(agent_id).join("memory");
        let root = tokio::fs::canonicalize(&memory_root)
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        let target = tokio::fs::canonicalize(memory_root.join(path))
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        if !target.starts_with(&root) {
            return Err(ApplicationError::Conflict);
        }
        let metadata = tokio::fs::symlink_metadata(&target)
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        if !metadata.file_type().is_file() || metadata.file_type().is_symlink() {
            return Err(ApplicationError::Conflict);
        }
        tokio::fs::read(target)
            .await
            .map_err(|_| ApplicationError::Internal)
    }

    async fn write_memory(
        &mut self,
        agent_id: AgentId,
        path: &Path,
        content: &[u8],
    ) -> Result<(), ApplicationError> {
        let memory_root = self.agent_home_for_id(agent_id).join("memory");
        let root = tokio::fs::canonicalize(&memory_root)
            .await
            .map_err(|_| ApplicationError::NotFound)?;
        let relative_parent = path.parent().unwrap_or_else(|| Path::new(""));
        let requested_parent = memory_root.join(relative_parent);
        create_private_dir(&requested_parent).await?;
        let parent = tokio::fs::canonicalize(&requested_parent)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        if !parent.starts_with(&root) {
            return Err(ApplicationError::Conflict);
        }
        let name = path.file_name().ok_or(ApplicationError::Conflict)?;
        let target = parent.join(name);
        if let Ok(metadata) = tokio::fs::symlink_metadata(&target).await
            && (!metadata.file_type().is_file() || metadata.file_type().is_symlink())
        {
            return Err(ApplicationError::Conflict);
        }
        write_private_file(&target, content).await
    }
}

async fn create_private_dir(path: &Path) -> Result<(), ApplicationError> {
    tokio::fs::create_dir_all(path)
        .await
        .map_err(|_| ApplicationError::Internal)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))
            .await
            .map_err(|_| ApplicationError::Internal)?;
    }
    Ok(())
}

async fn restrict_file(path: &Path) -> Result<(), ApplicationError> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))
            .await
            .map_err(|_| ApplicationError::Internal)?;
    }
    Ok(())
}

fn sanitize_codex_config(input: &toml::Table) -> Result<toml::Table, ApplicationError> {
    const ROOT_KEYS: &[&str] = &[
        "model_provider",
        "model",
        "model_reasoning_effort",
        "disable_response_storage",
    ];
    const PROVIDER_KEYS: &[&str] = &["name", "base_url", "wire_api", "requires_openai_auth"];

    let provider = input
        .get("model_provider")
        .and_then(toml::Value::as_str)
        .ok_or(ApplicationError::DriverUnavailable)?;
    let provider_input = input
        .get("model_providers")
        .and_then(toml::Value::as_table)
        .and_then(|providers| providers.get(provider))
        .and_then(toml::Value::as_table)
        .ok_or(ApplicationError::DriverUnavailable)?;
    let mut output = toml::Table::new();
    for key in ROOT_KEYS {
        if let Some(value) = input.get(*key) {
            output.insert((*key).to_owned(), value.clone());
        }
    }
    let mut sanitized_provider = toml::Table::new();
    for key in PROVIDER_KEYS {
        if let Some(value) = provider_input.get(*key) {
            sanitized_provider.insert((*key).to_owned(), value.clone());
        }
    }
    if !sanitized_provider.contains_key("base_url") {
        return Err(ApplicationError::DriverUnavailable);
    }
    output.insert(
        "model_providers".to_owned(),
        toml::Value::Table(toml::Table::from_iter([(
            provider.to_owned(),
            toml::Value::Table(sanitized_provider),
        )])),
    );
    Ok(output)
}

async fn write_private_file(path: &Path, contents: &[u8]) -> Result<(), ApplicationError> {
    let temporary = path.with_extension(format!("{}.tmp", Uuid::now_v7()));
    let mut options = tokio::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    options.mode(0o600);
    let mut file = options
        .open(&temporary)
        .await
        .map_err(|_| ApplicationError::Internal)?;
    file.write_all(contents)
        .await
        .map_err(|_| ApplicationError::Internal)?;
    file.sync_all()
        .await
        .map_err(|_| ApplicationError::Internal)?;
    drop(file);
    tokio::fs::rename(&temporary, path)
        .await
        .map_err(|_| ApplicationError::Internal)?;
    restrict_file(path).await
}

async fn write_new_private_file(path: &Path, contents: &[u8]) -> Result<(), ApplicationError> {
    let mut options = tokio::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    options.mode(0o600);
    let mut file = options.open(path).await.map_err(|error| {
        if error.kind() == std::io::ErrorKind::AlreadyExists {
            ApplicationError::Conflict
        } else {
            ApplicationError::Internal
        }
    })?;
    file.write_all(contents)
        .await
        .map_err(|_| ApplicationError::Internal)?;
    file.sync_all()
        .await
        .map_err(|_| ApplicationError::Internal)?;
    restrict_file(path).await
}

#[cfg(test)]
mod tests {
    use uuid::Uuid;

    use super::*;
    use crate::{
        computer::application::{DriverKind, LocalAgentState},
        ids::SpaceId,
    };

    #[tokio::test]
    async fn profile_has_one_filesystem_owner_and_retire_removes_only_target_home() {
        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let mut homes = AgentHomeAdapter::new(computer_home.clone(), None, None);
        let agent = LocalAgent {
            agent_id: AgentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(Uuid::now_v7()),
            name: "agent".to_owned(),
            handle: "agent".to_owned(),
            role_revision: 1,
            role: "role".to_owned(),
            driver: DriverKind::Codex,
            state: LocalAgentState::Active,
        };
        let agent_id = agent.agent_id;
        homes.provision(agent).await.unwrap();

        let profile_path = computer_home
            .join("agents")
            .join(agent_id.to_string())
            .join("profile.json");
        assert!(profile_path.is_file());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                std::fs::metadata(&profile_path)
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o600
            );
            assert_eq!(
                std::fs::metadata(profile_path.parent().unwrap())
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o700
            );
        }
        let first_fingerprint = homes.workspace_fingerprint(agent_id).await.unwrap();
        assert!(!first_fingerprint.is_empty());
        homes.suspend(agent_id).await.unwrap();
        assert_eq!(
            homes.agent(agent_id).await.unwrap().state,
            LocalAgentState::Suspended
        );
        homes.retire(agent_id).await.unwrap();
        assert!(!profile_path.exists());
        assert!(computer_home.exists());
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn attachment_files_stay_in_allowed_agent_directories() {
        use std::os::unix::fs::symlink;

        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let mut homes = AgentHomeAdapter::new(computer_home.clone(), None, None);
        let agent = LocalAgent {
            agent_id: AgentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(Uuid::now_v7()),
            name: "agent".to_owned(),
            handle: "agent".to_owned(),
            role_revision: 1,
            role: "role".to_owned(),
            driver: DriverKind::Codex,
            state: LocalAgentState::Active,
        };
        let agent_id = agent.agent_id;
        homes.provision(agent).await.unwrap();
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        tokio::fs::write(agent_home.join("workspace/result.txt"), b"result")
            .await
            .unwrap();

        let (name, content) = homes
            .read_attachment_source(agent_id, Path::new("workspace/result.txt"))
            .await
            .unwrap();
        assert_eq!(name, "result.txt");
        assert_eq!(content, b"result");
        assert!(matches!(
            homes
                .read_attachment_source(agent_id, Path::new("drivers/codex/auth.json"))
                .await,
            Err(ApplicationError::Conflict)
        ));

        let outside = directory.path().join("outside.txt");
        tokio::fs::write(&outside, b"secret").await.unwrap();
        symlink(&outside, agent_home.join("workspace/link.txt")).unwrap();
        assert!(matches!(
            homes
                .read_attachment_source(agent_id, Path::new("workspace/link.txt"))
                .await,
            Err(ApplicationError::Conflict)
        ));

        homes
            .write_attachment_output(agent_id, Path::new("workspace/download.txt"), b"download")
            .await
            .unwrap();
        assert!(matches!(
            homes
                .write_attachment_output(
                    agent_id,
                    Path::new("workspace/download.txt"),
                    b"overwrite"
                )
                .await,
            Err(ApplicationError::Conflict)
        ));
    }

    #[tokio::test]
    async fn codex_sources_are_explicit_sanitized_and_private() {
        #[cfg(unix)]
        use std::os::unix::fs::PermissionsExt;

        let directory = tempfile::tempdir().unwrap();
        let config_source = directory.path().join("config.toml");
        let auth_source = directory.path().join("auth.json");
        std::fs::write(
            &config_source,
            r#"
model_provider = "local"
model = "test-model"
project_trust = "trusted"

[model_providers.local]
name = "Local"
base_url = "https://provider.invalid"
wire_api = "responses"
env_key = "MUST_NOT_COPY"

[mcp_servers.private]
command = "must-not-copy"
"#,
        )
        .unwrap();
        std::fs::write(&auth_source, r#"{"OPENAI_API_KEY":"secret"}"#).unwrap();
        let computer_home = directory.path().join("computer");
        let mut homes = AgentHomeAdapter::new(
            computer_home.clone(),
            Some(config_source),
            Some(auth_source),
        );
        let agent = LocalAgent {
            agent_id: AgentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(Uuid::now_v7()),
            name: "agent".to_owned(),
            handle: "agent".to_owned(),
            role_revision: 1,
            role: "role".to_owned(),
            driver: DriverKind::Codex,
            state: LocalAgentState::Active,
        };

        homes.provision(agent.clone()).await.unwrap();

        let codex_home = homes.agent_home(&agent).join("drivers/codex");
        let installed = std::fs::read_to_string(codex_home.join("config.toml")).unwrap();
        assert!(installed.contains("test-model"));
        assert!(installed.contains("https://provider.invalid"));
        assert!(!installed.contains("project_trust"));
        assert!(!installed.contains("mcp_servers"));
        assert!(!installed.contains("MUST_NOT_COPY"));
        assert_eq!(
            std::fs::read(codex_home.join("auth.json")).unwrap(),
            br#"{"OPENAI_API_KEY":"secret"}"#
        );
        #[cfg(unix)]
        for path in [codex_home.join("config.toml"), codex_home.join("auth.json")] {
            assert_eq!(
                std::fs::metadata(path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
    }
}
