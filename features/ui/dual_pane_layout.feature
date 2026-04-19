Feature: Dual-pane ScreenLayout
  FlowState's ScreenLayout supports an optional dual-pane mode in which a
  primary chat pane occupies roughly 70% of the viewport width, while a
  secondary activity pane renders the remaining 30%. The activity pane
  is only shown when secondary content has been supplied. An operator
  may cycle the activity-timeline filter profile on demand by pressing
  Ctrl+T (P11); the pane itself remains visible across the cycle so the
  user always has somewhere to see the effect of their filter choice.

  Background:
    Given a ScreenLayout is initialised with a terminal size of 120x40

  @ui @dual-pane @wave1
  Scenario: ScreenLayout renders dual-pane 70/30 when secondary content is set
    Given the primary content is "primary pane body"
    And the secondary content is "activity pane body"
    When the ScreenLayout is rendered
    Then the rendered output should contain two panes side by side
    And the primary pane should occupy roughly 70% of the width
    And the secondary pane should occupy roughly 30% of the width

  @ui @dual-pane @wave1
  Scenario: ScreenLayout renders single-pane when secondary content is empty
    Given the primary content is "primary pane body"
    And the secondary content is empty
    When the ScreenLayout is rendered
    Then the rendered output should contain a single pane
    And the primary pane should occupy the full width

  @ui @dual-pane @wave1 @p11
  Scenario: Operator presses Ctrl+T and the activity pane remains visible
    Given the primary content is "primary pane body"
    And the secondary content is "activity pane body"
    And the activity pane is visible
    When the operator presses Ctrl+T
    Then the activity pane should be visible
    And the primary pane should occupy roughly 70% of the width
    And the secondary pane should occupy roughly 30% of the width

  @ui @dual-pane @wave1 @p11
  Scenario: Operator cycles Ctrl+T three times and returns to the default profile
    Given the primary content is "primary pane body"
    And the secondary content is "activity pane body"
    And the activity pane is visible
    When the operator presses Ctrl+T three times
    Then the activity pane should be visible
    And the primary pane should occupy roughly 70% of the width
    And the secondary pane should occupy roughly 30% of the width

  @ui @dual-pane @wave1 @primary-width-bug
  Scenario Outline: Primary pane content fits the 70% column budget across the ADR contract matrix
    ADR — Chat Swarm Dual-Pane Layout — width contract:

        available  = W - 1
        primary    = (available * 7) / 10
        secondary  = available - primary

    Contract matrix:
      | W   | Primary | Sep | Secondary |
      | 80  | 55      | 1   | 24        |
      | 100 | 69      | 1   | 30        |
      | 120 | 83      | 1   | 36        |

    Invariant: "All primary content (messages, status bar, input, session
    trail) must be sized to Primary, not to TerminalInfo.Width." Any row
    that exceeds the primary budget means the chat Intent is rendering
    primary content at the full terminal width and relying on lipgloss
    hard-wrap to trim it — the exact regression the contract forbids.
    Given a chat Intent sized to <width>x40 with the activity pane visible
    When the chat Intent view is rendered
    Then every primary-column line should be at most the 70% primary width

    Examples:
      | width |
      | 80    |
      | 100   |
      | 120   |

  @ui @dual-pane @wave1 @primary-width-bug
  Scenario Outline: Short primary input stays a single rendered row across the ADR contract matrix
    Regression guard: if primary content is rendered wider than the primary
    column, lipgloss Width() hard-wraps each line, silently doubling the
    rendered row count. A short known single-line input ("hello") must
    remain exactly one row at every width in the contract matrix.
    Given a chat Intent sized to <width>x40 with the activity pane visible
    And the chat input is set to "hello"
    When the chat Intent view is rendered
    Then the primary input row should render as a single row

    Examples:
      | width |
      | 80    |
      | 100   |
      | 120   |

  @ui @dual-pane @wave1 @primary-width-bug @single-pane-fallback
  Scenario: Single-pane fallback at W=79 renders full width with no separator
    ADR contract matrix final row:
      | 79  | — single-pane fallback — |

    Below the dualPaneMinWidth threshold of 80, the chat Intent MUST
    render as single-pane: no dual-pane separator glyph may appear as a
    structural column marker, and no Activity Timeline heading may be
    emitted. Verifies the fallback is exercised end-to-end via
    chat.Intent.View rather than a direct ScreenLayout call.
    Given a chat Intent sized to 79x40 with the activity pane visible
    When the chat Intent view is rendered
    Then the rendered chat Intent view should not contain the dual-pane separator
    And the rendered chat Intent view should not contain the Activity Timeline header

  @ui @dual-pane @wave1 @footer-width-invariant
  Scenario Outline: Footer separator and help text stay within terminal width
    A rendered row that exceeds terminal width wraps in the terminal
    emulator and shifts the dual-pane separator column off-grid on the
    wrapped continuation — the Activity Timeline pane then appears to
    "shrink" because the right half of every over-wide row bleeds into
    the next display row's primary column.

    Two footer-side offenders must NOT produce over-wide rows:

      1. `buildFooterParts` separator: previously hardcoded to 100 glyphs
         regardless of TerminalInfo.Width, making the footer 20 cells wider
         than an 80-column terminal and 20 cells narrower than a 120-column
         one.

      2. The chat intent status-bar hint (chatHintSuffix) is a fixed
         157-cell string advertising Ctrl+G/Ctrl+E/Ctrl+T and friends; at
         W in {80, 100, 120, 140} it overflows. Truncation was rejected
         because it silently drops Ctrl+C: quit / Ctrl+T hints (violating
         the swarm-activity-toggle hint assertion). Word-wrap preserves
         every hint while keeping every rendered row <= W.
    Given a chat Intent sized to <width>x40 with the activity pane visible
    When the chat Intent view is rendered
    Then every rendered row should be at most <width> cells wide
    And the help hints "Ctrl+C", "Ctrl+T" should remain visible

    Examples:
      | width |
      | 80    |
      | 100   |
      | 120   |
      | 140   |
