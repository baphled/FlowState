---
name: ai-commit
description: Create a FlowState commit after checking current Dropbox instructions and external project metadata.
---

Read root AGENTS.md and the required current Dropbox records before committing.
Store attribution, qualifications and receipts under `/FlowState/Metadata`, outside
the repository. Resolve actual identities from runtime evidence; use `unverified`
where unavailable. Never infer a reviewer from Git configuration or guess a model.

Stage only source, tests and reusable project files. Keep captures/reports outside
the checkout. Run the required checks and hooks. Use an external read-back copy
of the current Dropbox metadata record:

```bash
PROJECT_METADATA_FILE=/outside/checkout/record.json make ai-commit FILE=/outside/checkout/message.txt
```

The helper validates the staged tree and external record shape, then adds only a
neutral Project-Metadata Dropbox reference. It does not authenticate remote reads
or serving identity. Do not add AI/model/reviewer/coauthor trailers, skip hooks or
rewrite published history to change attribution. Correct the external record by
publishing a new immutable attempt and record the superseded reference in Dropbox.
