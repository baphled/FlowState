# NPR Onboarding Swarm

Interactive MVP port of the Fsonealphabaseas onboarding flow into FlowState swarm
configuration. The swarm guides a user through the six-stage onboarding
conversation, synthesises the transcript into an `NPRProfile`, and validates the
final JSON against `npr-profile-v01`.

The schema is derived from:

`Fsonealphabaseas/src/lib/types/npr.ts`

## Components

- `swarms/npr-onboarding.yml` - swarm manifest and final schema gate
- `agents/npr-onboarding-lead.md` - conversation/state coordinator
- `agents/npr-profile-synthesizer.md` - final `NPRProfile` JSON producer
- `agents/npr-quality-reviewer.md` - evidence/safety reviewer and summary writer
- `schemas/npr-profile-v01.json` - JSON schema matching the TypeScript NPR type

`agents/npr-stage-interviewer.md` is retained as an optional future specialist,
but the default MVP swarm keeps live intake in the lead agent so each user reply
can be handled without a nested delegation round trip.

## Install

From the FlowState repo root:

```bash
cp -r examples/swarms/npr-onboarding/* ~/.config/flowstate/
flowstate agents refresh
flowstate swarms refresh
```

## Validate

```bash
flowstate swarm validate npr-onboarding
```

## Try It

For interactive use, start from chat/TUI with:

```text
@npr-onboarding Start a new NPR onboarding for userId=example-user
```

For a CLI smoke test:

```bash
flowstate run --agent npr-onboarding "Start a new NPR onboarding for userId=example-user"
```

The first turn should initialise the session and ask the first onboarding
question. Continue the conversation in the same chat/session so the lead can
append answers to the transcript and advance through the six stages.

## MVP Boundaries

This MVP is ready to test as a FlowState conversation/orchestration package. It
does not yet include the production Fsone features around Akilii UI state,
voice/TTS, Whisper, Merkle sealing, deterministic server-side question-count
guards, psychometric scoring routes, or NPR vault persistence.

Those can be added later as custom FlowState tools or gates once the
conversation and profile shape are working.
