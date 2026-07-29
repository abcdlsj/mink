//! 新 Server 运行时的 crate 内 facade。内部层不能由其他顶层模块直接引用。

mod adapters;
mod application;
mod domain;

pub(crate) fn write_browser_openapi() -> anyhow::Result<()> {
    adapters::openapi::write_browser_openapi()
}
