//! Driver prompt template assembly.
//!
//! The Sumi contracts and turn assembly live in the `sumi-builtin-agent`
//! harness crate; this module re-exports them so both the builtin and codex
//! drivers share one entry point.

pub(in crate::computer) use sumi_builtin_agent::{
    codex_turn_instruction, driver_contract_hash, product_contract, system_messages,
    turn_instruction,
};
