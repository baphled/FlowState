# Testing workflow — standard Go

Use standard Go `testing` unit, table-driven, integration and headless CLI tests.
Cucumber, Godog, Gherkin `.feature` files and step-definition suites are forbidden.
Do not add their dependencies, install their runners or request them in a handoff.
BDD as a requirement to define observable behaviour does not require a framework.

For a bug fix, reproduce the failure in a focused Go regression test, implement
and run it to green. Reuse coverage rather than duplicating assertions. Use local
fake providers, temporary databases and httptest servers; real inference is not
routine CI proof. Validate workflows/configuration directly for tooling changes.

`make check` runs formatting, vet/lint and Go tests. The following metadata-guard
slice adds policy checks and Python regression tests. Run Go race checks
where required by the actual packet. Existing `make bdd*` aliases fail explicitly.
CI must pass for the exact candidate before merge. Raw output and acceptance
records are published/read back in Dropbox Metadata, not committed into Git.
