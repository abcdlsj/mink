//! 新 Server 运行时的 crate 内 facade。内部层不能由其他顶层模块直接引用。

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
