mod adapters;
mod application;
mod domain;

use crate::{cli::ServerArgs, config::load};

pub(crate) async fn run(args: ServerArgs) -> anyhow::Result<()> {
    let config = load(args.config.as_ref())?;
    adapters::runtime::run(config.server).await
}

pub(crate) fn write_browser_openapi() -> anyhow::Result<()> {
    adapters::openapi::write_browser_openapi()
}
