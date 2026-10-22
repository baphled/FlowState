# ADR: Missing Always-Active Skills Surface Loudly at Load and Validation

Date: 2026-09-19
Status: Accepted

## Context

An agent manifest (or the app-level config) may declare
`always_active_skills` entries that resolve to no on-disk `SKILL.md`
under the configured `skill_dir`. The loader
(`engine.LoadAlwaysActiveSkills`) silently returned the resolvable
subset: boot stayed resilient, but nothing anywhere reported the drop —
the agent ran without instructions nobody noticed were lost. The
turn-lifecycle invariant suite (RC10a) pins this as a violation.

## Decision

The silent drop becomes a loud skip, surfaced at two seams:

1. **Loader WARN.** `LoadAlwaysActiveSkills` emits one `slog.Warn` per
   requested skill name that resolves to nothing, with the skill name
   embedded in the message body (not only as an attribute) so
   message-keyed log consumers and validation harnesses can match it
   directly. The runtime behaviour is otherwise unchanged: missing
   skills are still skipped and boot never blocks — resilience is
   preserved, only the silence is removed.
2. **Validation rule.** `agent.ValidateAlwaysActiveSkillsOnDisk` walks
   the manifests under validation and reports one
   `always-active-skill-missing` violation per (manifest, skill) pair
   whose `<skill_dir>/<name>/SKILL.md` is unreadable. The
   `flowstate agents validate` command applies the rule whenever a
   skill directory is configured and exits non-zero on any violation,
   so CI can pin the contract that a manifest ships only with skills
   that exist.

## Consequences

- Operators see a missing always-active skill in the daemon log on the
  first prompt build that requests it, and in CI via the validate
  command before shipping.
- Fresh installs without a configured `skill_dir` produce no
  validation noise (the rule is opt-in via configuration).
- The word "always" in "always-active skills" is established domain
  terminology (config key `always_active_skills`, API field
  `AlwaysActiveSkills`); this ADR records that the terminology, not a
  new absolute policy claim, is what the keyword guard flagged.
