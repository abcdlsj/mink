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
}

impl AgentHomeAdapter {
    pub(in crate::computer) fn new(computer_home: PathBuf) -> Self {
        Self { computer_home }
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

    fn agent_home_for_id(&self, agent_id: AgentId) -> PathBuf {
        self.computer_home.join("agents").join(agent_id.to_string())
    }
}

#[async_trait(?Send)]
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
        let mut homes = AgentHomeAdapter::new(computer_home.clone());
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
}
