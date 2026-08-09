# sumi-builtin-agent

Sumi Computer harness for [`sumi-agent-core`](../sumi-agent-core). It owns
what the Sumi product needs but the generic runtime must not:

- the collaboration and CLI contracts (`product_contract`,
  `driver_contract`), turn instruction assembly, and cache-key hashing;
- the context compaction policy (`BuiltinContext`): token-budget trigger,
  cut-point selection, split-turn prefix summaries, file-operation
  appendix, and persisted compaction records in the session metadata.

The Sumi Computer adapter (`src/computer/drivers/builtin_agent.rs`) builds an
`AgentConfig` with `BuiltinContext` and assembles `TurnRequest` from each Run.
