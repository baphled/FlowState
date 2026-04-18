# TUI Keybindings

FlowState's chat TUI uses a small, curated set of Ctrl-prefixed shortcuts. This
reference lists them with a one-line note where the binding shadows another
tool's convention, so users know to expect collisions and have somewhere to
point when reporting "the key does nothing".

## Reference

| Binding | Action | Notes |
|---------|--------|-------|
| `Enter` | Send message | |
| `Alt+Enter` | New line in composer | |
| `Tab` | Toggle active agent | |
| `Esc` | Dismiss modal / picker / session viewer | Double-tap cancels in-flight stream (P1). |
| `Ctrl+C` | Cancel stream, save session, quit | Standard interrupt; also the terminal SIGINT. |
| `Ctrl+D` | Open delegation picker | |
| `Ctrl+A` | Open agent picker | |
| `Ctrl+P` | Open model selector | |
| `Ctrl+S` | Open session browser | May freeze on terminals with flow control; run `stty -ixon` once per shell. |
| `Ctrl+G` | Open session tree | |
| `Ctrl+E` | Open event details modal | **Collision warning** — see below. |
| `Ctrl+T` | Cycle activity-timeline filter profile | **Collision warning** — see below. P11 repurposed this from pane-toggle. |
| `Up` / `Down` | Scroll viewport line by line | |
| `PgUp` / `PgDn` | Scroll viewport, or event-details modal, by page | P8 T1: modal now supports pagination. |
| `Home` / `End` | Jump to top / bottom of viewport or modal | P8 T1: modal now supports these. |

## Collision notes

### `Ctrl+T`

- **tmux** uses `Ctrl+B t` as the default prefix for clock, but many users
  remap to `Ctrl+A` or `Ctrl+T`. If you've rebound tmux to `Ctrl+T`, it will
  intercept the key before FlowState sees it. Configure tmux with a different
  prefix, or accept that the activity-timeline filter cycle is unavailable
  inside that pane.
- **GNU Screen** passes `Ctrl+T` through by default, so no collision.
- **Many IDEs** (VS Code, JetBrains, Sublime) bind `Ctrl+T` to "quick open by
  symbol". That matters only when running FlowState inside an IDE's
  integrated terminal; the IDE's binding usually wins unless the terminal is
  in focus-follows-input mode.

### `Ctrl+E`

- **Readline** (which most shells use by default) binds `Ctrl+E` to
  "move to end of line". Inside FlowState the chat composer is Bubble Tea, not
  readline, so the chat capture of `Ctrl+E` is clean — but shell-integrated
  popovers (fzf overlays, some shell widgets) may still intercept.
- **Emacs** and **Emacs-mode shells** bind `Ctrl+E` to "end of line"; same
  caveat as readline.
- **IDE terminals**: VS Code sometimes binds `Ctrl+E` to "go to file" when
  the terminal is not in focus.

### What to do if a binding is intercepted

1. Check the surrounding environment (tmux, IDE, shell binding) and unbind or
   rebind there.
2. Until we add a user-facing keymap override (future work; out of P8 scope),
   the bindings are fixed in code at
   `internal/tui/intents/chat/intent.go` (`handleInputKey`, `handleGlobalKey`)
   and `internal/tui/intents/eventdetails/intent.go` (`handleKey`).
