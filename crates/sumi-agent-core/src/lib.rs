//! Portable builtin LLM agent runtime.
//!
//! The runtime runs an OpenAI-compatible chat stream through an agent loop with
//! sandboxed file and shell tools, persists provider sessions, and exposes a
//! plugin API for external conversation channels.

mod agent;
mod config;
pub mod context;
mod engine;
mod memory;
mod plugin;
mod provider;
mod sandbox;
mod session;
mod tool_executor;
mod types;
mod workspace;

pub use agent::{AgentError, AgentRuntime, Completion, TurnOutcome, TurnRequest};
pub use config::{AgentConfig, SandboxConfig};
pub use context::{
    ContextStrategy, CutPoint, IdentityContext, Summarizer, estimate_messages,
    file_operations_appendix, find_cut_point,
};
pub use memory::{MemoryFile, PRIMARY_MEMORY_PATH};
pub use plugin::{AgentPlugin, PluginContext};
pub use provider::ProviderConfig;
pub use sandbox::SandboxAdapter;
pub use session::Session;
pub use types::{Attachment, Message, ToolDef};
pub use types::TokenUsage;
pub use workspace::agent_rooted_path;
