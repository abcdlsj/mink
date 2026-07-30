mod adapters;
mod application;
mod domain;

pub(crate) async fn run(args: crate::cli::ServerArgs) -> anyhow::Result<()> {
    let config = crate::config::load(args.config.as_ref())?;
    adapters::runtime::run(config.server).await
}

pub(crate) fn write_browser_openapi() -> anyhow::Result<()> {
    adapters::openapi::write_browser_openapi()
}
