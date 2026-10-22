# Post-MVP spike: real-time conversational voice

Status: proposed spike **after the first working dogfood MVP** (owner direction, 22 October 2026). This is not a prerequisite for initial SDLC delivery, a commitment to a specific provider, or authorisation to create a separate always-on service.

## Problem and target experience

The user wants a natural two-way spoken conversation with a FlowState agent comparable to a modern interactive voice assistant: speaks responses aloud, accepts follow-up turns naturally, supports interruption and contextual continuity. Speech-to-text plus a text-only agent response is **not** the target. The present FlowState voice experience is owner-reported broken/poor, apparently using transcription and possibly an additional service; these architecture guesses remain unverified.

## Spike questions

1. Inspect the exact running voice implementation, deployment routes, and vault documentation. Identify actual speech recognition, audio output/TTS, turn detection, streaming, transport, and session context ownership; do not infer architecture from UI behaviour.
2. Compare minimal end-to-end designs for streaming speech-in and speech-out, including latency, hands-free turn-taking, interruption/barge-in, recovery and provider-independent session integration. Determine which interfaces are reusable from the common session engine and which are voice-specific adapters.
3. Measure real voice latency and perceived conversation quality on representative dialogues. Instrument transcription, agent turnaround, first audible response, interruption stop time and reconnect/recovery. Clearly separate measured outcomes from model/provider hypotheses.
4. Keep the existing engine's permissions, tool effects, delegation, cancellation, provenance, budget and durable turn contracts. Voice must be another access surface, not an alternate agent runtime or an unapproved background service.
5. Propose one thin spike and a falsifiable demo: a multi-turn voice conversation where the agent speaks back audibly, maintains context, can be interrupted, and resumes after normal reconnect without duplicating tool effects.

## Acceptance for the spike

- User hears agent responses through the selected client, not only reads transcripts.
- Two-way interaction works without repeatedly manually starting independent requests.
- Interruption stops or supersedes outgoing speech safely; stale voice responses are not presented as the current turn.
- Session/tool identity and policy parity hold with text interfaces.
- Explicit measurement and limitations identify whether the experience is good enough to proceed, redesign, or defer.

No vendor or technology chosen by this note; do not move this into initial MVP scope.
