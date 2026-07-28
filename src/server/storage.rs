use std::sync::Arc;

use anyhow::{Context, Result};
use object_store::{ObjectStore, aws::AmazonS3Builder, local::LocalFileSystem};

use crate::config::ServerConfig;

pub fn build(config: &ServerConfig) -> Result<Arc<dyn ObjectStore>> {
    if let Some(s3) = &config.attachment_s3 {
        let mut builder = AmazonS3Builder::from_env()
            .with_bucket_name(&s3.bucket)
            .with_region(&s3.region)
            .with_allow_http(s3.allow_http);
        if let Some(endpoint) = &s3.endpoint {
            builder = builder.with_endpoint(endpoint);
        }
        return Ok(Arc::new(builder.build().context(
            "failed to configure S3-compatible Attachment storage",
        )?));
    }

    std::fs::create_dir_all(&config.attachment_dir)
        .context("failed to create local Attachment directory")?;
    Ok(Arc::new(
        LocalFileSystem::new_with_prefix(&config.attachment_dir)
            .context("failed to configure local Attachment storage")?,
    ))
}
