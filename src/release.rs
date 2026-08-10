use std::path::Path;

use anyhow::{Context, ensure};
use base64::{Engine, engine::general_purpose::STANDARD};
use ed25519_dalek::{Signer, SigningKey};
use rand::{RngCore, rngs::OsRng};
use sha2::{Digest, Sha256};
use tokio::io::AsyncWriteExt;

use crate::{
    cli::{ComputerReleaseArgs, ReleaseKeygenArgs},
    protocol::update::{ComputerRelease, SignedComputerRelease, current_target},
};

const MAX_ARTIFACT_BYTES: usize = 200 * 1024 * 1024;

pub(crate) async fn keygen(args: ReleaseKeygenArgs) -> anyhow::Result<()> {
    let mut seed = [0_u8; 32];
    OsRng.fill_bytes(&mut seed);
    let signing = SigningKey::from_bytes(&seed);
    write_key_file(&args.private_key, STANDARD.encode(seed).as_bytes(), true).await?;
    write_key_file(
        &args.public_key,
        STANDARD
            .encode(signing.verifying_key().to_bytes())
            .as_bytes(),
        false,
    )
    .await?;
    println!("{}", args.public_key.display());
    Ok(())
}

pub(crate) async fn computer(args: ComputerReleaseArgs) -> anyhow::Result<()> {
    semver::Version::parse(&args.version).context("Computer release version must be SemVer")?;
    ensure!(
        args.protocol_version > 0,
        "protocol version must be positive"
    );
    ensure!(
        args.artifact.is_file(),
        "Computer release artifact must be a regular file"
    );
    validate_private_key_permissions(&args.private_key)?;

    let key = tokio::fs::read_to_string(&args.private_key)
        .await
        .context("failed to read Computer release private key")?;
    let seed: [u8; 32] = STANDARD
        .decode(key.trim())
        .context("Computer release private key is not base64")?
        .try_into()
        .map_err(|_| anyhow::anyhow!("Computer release private key must be 32 bytes"))?;
    let signing = SigningKey::from_bytes(&seed);
    let artifact_content = tokio::fs::read(&args.artifact).await?;
    ensure!(
        artifact_content.len() <= MAX_ARTIFACT_BYTES,
        "Computer release artifact is too large"
    );
    let target = args.target.unwrap_or_else(|| current_target().to_owned());
    ensure!(
        target != "unsupported",
        "this build target is not supported for Computer releases"
    );
    ensure!(
        target
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-'),
        "Computer release target is invalid"
    );
    let artifact_name = format!("sumi-{}-{target}", args.version);
    let release = ComputerRelease {
        version: args.version,
        protocol_version: args.protocol_version,
        target,
        artifact: artifact_name.clone(),
        sha256: hex::encode(Sha256::digest(&artifact_content)),
    };
    let signed = SignedComputerRelease {
        signature: STANDARD.encode(signing.sign(&release.signing_bytes()?).to_bytes()),
        release,
    };

    ensure_private_directory(&args.output_dir).await?;
    let artifact_output = args.output_dir.join(&artifact_name);
    write_release_file(&artifact_output, &artifact_content).await?;
    write_atomically(
        &args.output_dir.join("manifest.json"),
        &serde_json::to_vec_pretty(&signed)?,
    )
    .await?;
    println!("{}", args.output_dir.display());
    Ok(())
}

#[cfg(unix)]
fn validate_private_key_permissions(path: &Path) -> anyhow::Result<()> {
    use std::os::unix::fs::PermissionsExt;

    let metadata = std::fs::symlink_metadata(path)?;
    ensure!(
        metadata.file_type().is_file(),
        "Computer release private key must be a regular file"
    );
    ensure!(
        metadata.permissions().mode() & 0o077 == 0,
        "Computer release private key must not be accessible by group or other users"
    );
    Ok(())
}

#[cfg(not(unix))]
fn validate_private_key_permissions(path: &Path) -> anyhow::Result<()> {
    ensure!(
        path.is_file(),
        "Computer release private key must be a regular file"
    );
    Ok(())
}

async fn ensure_private_directory(path: &Path) -> anyhow::Result<()> {
    tokio::fs::create_dir_all(path).await?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700)).await?;
    }
    Ok(())
}

async fn write_release_file(path: &Path, content: &[u8]) -> anyhow::Result<()> {
    match tokio::fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(path)
        .await
    {
        Ok(mut file) => {
            file.write_all(content).await?;
            file.sync_all().await?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                file.set_permissions(std::fs::Permissions::from_mode(0o700))
                    .await?;
            }
            Ok(())
        }
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {
            let existing = tokio::fs::read(path).await?;
            ensure!(
                existing == content,
                "an immutable Computer release artifact already exists with different content"
            );
            Ok(())
        }
        Err(error) => Err(error.into()),
    }
}

async fn write_key_file(path: &Path, content: &[u8], private: bool) -> anyhow::Result<()> {
    let mut file = tokio::fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(path)
        .await?;
    file.write_all(content).await?;
    file.write_all(b"\n").await?;
    file.sync_all().await?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = if private { 0o600 } else { 0o644 };
        file.set_permissions(std::fs::Permissions::from_mode(mode))
            .await?;
    }
    Ok(())
}

async fn write_atomically(path: &Path, content: &[u8]) -> anyhow::Result<()> {
    let pending = path.with_extension("json.new");
    let mut file = tokio::fs::File::create(&pending).await?;
    file.write_all(content).await?;
    file.sync_all().await?;
    drop(file);
    tokio::fs::rename(pending, path).await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signature, Verifier, VerifyingKey};
    use tempfile::tempdir;

    #[tokio::test]
    async fn keygen_writes_a_matching_private_key_with_private_permissions() {
        let directory = tempdir().unwrap();
        let private_key = directory.path().join("release.key");
        let public_key = directory.path().join("release.pub");

        keygen(ReleaseKeygenArgs {
            private_key: private_key.clone(),
            public_key: public_key.clone(),
        })
        .await
        .unwrap();

        let seed: [u8; 32] = STANDARD
            .decode(
                tokio::fs::read_to_string(&private_key)
                    .await
                    .unwrap()
                    .trim(),
            )
            .unwrap()
            .try_into()
            .unwrap();
        let public = tokio::fs::read_to_string(public_key).await.unwrap();
        assert_eq!(
            public.trim(),
            STANDARD.encode(SigningKey::from_bytes(&seed).verifying_key().to_bytes())
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                tokio::fs::metadata(private_key)
                    .await
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o600
            );
        }
    }

    #[tokio::test]
    async fn computer_release_command_writes_a_verifiable_immutable_release() {
        let directory = tempdir().unwrap();
        let artifact = directory.path().join("sumi");
        tokio::fs::write(&artifact, b"computer binary")
            .await
            .unwrap();
        let private_key = directory.path().join("release.key");
        tokio::fs::write(&private_key, STANDARD.encode([5_u8; 32]))
            .await
            .unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            tokio::fs::set_permissions(&private_key, std::fs::Permissions::from_mode(0o600))
                .await
                .unwrap();
        }
        let output = directory.path().join("stable");
        computer(ComputerReleaseArgs {
            artifact,
            version: "1.2.3".into(),
            protocol_version: 4,
            target: Some("aarch64-apple-darwin".into()),
            private_key,
            output_dir: output.clone(),
        })
        .await
        .unwrap();

        let manifest: SignedComputerRelease =
            serde_json::from_slice(&tokio::fs::read(output.join("manifest.json")).await.unwrap())
                .unwrap();
        let signature: [u8; 64] = STANDARD
            .decode(manifest.signature)
            .unwrap()
            .try_into()
            .unwrap();
        VerifyingKey::from_bytes(
            &SigningKey::from_bytes(&[5_u8; 32])
                .verifying_key()
                .to_bytes(),
        )
        .unwrap()
        .verify(
            &manifest.release.signing_bytes().unwrap(),
            &Signature::from_bytes(&signature),
        )
        .unwrap();
        assert_eq!(
            tokio::fs::read(output.join(&manifest.release.artifact))
                .await
                .unwrap(),
            b"computer binary"
        );
    }
}
