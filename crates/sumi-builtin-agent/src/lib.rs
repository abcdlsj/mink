//! Sumi Computer harness for the portable builtin agent core.
//!
//! This crate owns what the Sumi product needs but the generic core must not:
//! the collaboration and CLI contracts, turn instruction assembly, and the
//! context compaction policy. `sumi-agent-core` only runs the provider loop.

pub mod compaction;
pub mod prompt;

pub use compaction::{BuiltinContext, CompactionConfig};
pub use prompt::{
    codex_turn_instruction, driver_contract, driver_contract_hash, product_contract,
    system_messages, turn_instruction,
};
