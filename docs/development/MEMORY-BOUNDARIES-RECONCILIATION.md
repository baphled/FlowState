# Memory, operational state and recursive execution — design reconciliation

Status: **design questions captured, not implementation decisions** (22 October 2026). These are follow-ups to the existing greenfield roadmap and must be reconciled with canonical FlowState vault ADRs before contracts are frozen. This note is not permission to build new infrastructure or block first dogfood.

## Existing roadmap references
- P09/T040: durable coordination, leases, task handoffs, checkpoints, background work. Its scope is **operational execution state**; `coordination store` is an imprecise label. `Execution State Store` is a candidate name, not an approved rename.
- P06/T028–T031: request accounting, bounded context views, compaction/re-hydration, shared budgets.
- P13/T055–T057: optional memory, Qdrant recall, learning, vault indexing and UI operations.
- No explicit Recursive Language Model (RLM) execution contract was identified in the reviewed tasks. Compaction is not RLM.

## Questions to resolve with vault evidence
1. Separate operational state, transient working memory, durable semantic/episodic knowledge, source documents, retrieval indexes and context views. Which component owns each authoritative record, and which are derived projections?
2. Which records require retention beyond the run? Define provenance, lifecycle, expiry, correction, deletion, deduplication, permissions and recovery per class without assuming a single storage backend.
3. Specify storage-independent read/write/query interfaces and source references. Qdrant/vector retrieval, vault documents and operational persistence may coexist; their existence does not establish a common authority or identical durability.
4. Examine existing coordination-store contracts and vault ADRs before naming/renaming services. Avoid conflating orchestration leases with long-term memory.
5. Clarify whether RLM is a required capability, how recursive decomposition/interpreter/tool calls differ from ordinary delegation and compaction, and which shared execution, context, budget and gate contracts it reuses. Record this as research/design until evidence and product acceptance exist.

## Reusable learning/knowledge workflow candidate
The process should be configurable across domains and storage adapters: discover candidate information → evaluate its lasting value and authority → reconcile duplicates and contradictory evidence → promote/update an authorised destination → verify recorded changes → apply retention/cleanup policy. Dropbox and the Obsidian vault are one example of short-term workspace and durable documentation; neither is a hardcoded memory tier. The execution state store and vector index are separate candidates with different semantics. Curator/vault specialists should use actual vault conventions and ADRs rather than inventing destinations.

## Delivery policy
For first dogfood retain minimal, reliable execution persistence, provenance, bounded context and restart/recovery; do **not** make RLM, vector integration, memory promotion or full vault sync prerequisites. Reconcile this note into the existing P06/P09/P13 requirements and canonical ADRs, with the designated knowledge curator, in small reviewed changes. No unsupported 'implemented', 'verified', or 'promoted' claims. Scratchpad cleanup may remove redundant transfer snapshots only after checking that unique source evidence and any relevant findings are retained.
