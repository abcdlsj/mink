//! Portable builtin LLM agent runtime.
//!
//! The runtime runs an OpenAI-compatible chat stream through an agent loop with
//! sandboxed file and shell tools, persists provider sessions, and exposes a
//! plugin API for external conversation channels.

mod agent;
mod config;
mod engine;
mod memory;
mod plugin;
mod prompt;
mod provider;
mod sandbox;
mod session;
mod tool_executor;
mod types;
mod workspace;

pub use agent::{AgentError, AgentRuntime, Completion, TurnOutcome, TurnRequest};
pub use config::{AgentConfig, CompactionConfig, SandboxConfig};
pub use memory::{MemoryFile, PRIMARY_MEMORY_PATH};
pub use plugin::{AgentPlugin, PluginContext};
pub use provider::ProviderConfig;
pub use sandbox::SandboxAdapter;
pub use types::{Attachment, Message, ToolDef};
pub use workspace::agent_rooted_path;
