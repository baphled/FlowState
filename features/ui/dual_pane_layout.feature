Feature: Dual-pane ScreenLayout
  """
  FlowState's ScreenLayout supports an optional dual-pane mode in which a
  primary chat pane occupies roughly 70% of the viewport width, while a
  secondary activity pane renders the remaining 30%. The activity pane
  is only shown when secondary content has been supplied. An operator
  may also collapse the activity pane on demand by pressing Ctrl+T,
  and restore it by pressing Ctrl+T again. This feature captures the
  behaviour of that layout and its toggle.
  """

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

  @ui @dual-pane @wave1
  Scenario: Operator presses Ctrl+T and the activity pane hides
    Given the primary content is "primary pane body"
    And the secondary content is "activity pane body"
    And the activity pane is visible
    When the operator presses Ctrl+T
    Then the activity pane should be hidden
    And the primary pane should occupy the full width

  @ui @dual-pane @wave1
  Scenario: Operator presses Ctrl+T again and the activity pane is restored
    Given the primary content is "primary pane body"
    And the secondary content is "activity pane body"
    And the activity pane has been hidden via Ctrl+T
    When the operator presses Ctrl+T
    Then the activity pane should be visible
    And the primary pane should occupy roughly 70% of the width
    And the secondary pane should occupy roughly 30% of the width
