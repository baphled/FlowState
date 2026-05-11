# Render Deployment

FlowState can be deployed as a single Render web service:

- Docker builds the Vue UI from `web/`.
- Docker builds the Go server from `cmd/flowstate`.
- The Go server serves both `/api/...` and the built Vue app from `/`.

## Blueprint

Use the root `render.yaml` as a Render Blueprint. It creates a free Docker
web service named `flowstate-npr-onboarding` and prompts for
`ANTHROPIC_API_KEY` during initial setup.

The container starts FlowState with:

```bash
/app/flowstate serve --host 0.0.0.0 --port ${PORT:-10000}
```

`FLOWSTATE_WEB_DIST_DIR=/app/web/dist` tells the API server where the Vue
build lives.

## NPR Onboarding

The NPR onboarding assets are bundled with the binary and seeded on startup:

- `npr-onboarding-lead`
- `npr-profile-synthesizer`
- `npr-quality-reviewer`
- `npr-onboarding`
- `npr-profile-v01`

This means a fresh Render instance can resolve `@npr-onboarding` without
manual copying into `~/.config/flowstate`.

## Free Tier Notes

The blueprint uses Render's free instance type for temporary testing.
Free instances spin down when idle and use an ephemeral filesystem, so local
sessions and coordination-store state should be treated as disposable.
