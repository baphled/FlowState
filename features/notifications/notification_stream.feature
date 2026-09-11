# Feature: Notification event stream
#   The backend surfaces turn lifecycle and provider health signals as
#   NotificationEvent bus events and streams them to clients over the
#   /api/v1/notifications/events SSE endpoint.
#
Feature: Notification event stream
  Background:
    Given the event bus is running
    And the notification SSE endpoint is available at "/api/v1/notifications/events"

  Scenario: Turn completion publishes a notification event
    When a delegated turn completes successfully
    Then a notification event of type "turn_complete" is published
    And the notification severity is "info"

  Scenario: Task failure publishes a notification event with a reason
    When a session task fails with reason "provider unavailable"
    Then a notification event of type "task_failed" is published
    And the notification severity is "error"
    And the notification message contains "provider unavailable"

  Scenario: Provider cooldown escalation publishes a notification event
    When a provider enters a cooldown
    Then a notification event of type "cooldown" is published
    And the notification severity is "warning"

  Scenario: Provider failover publishes a notification event
    When the active provider fails over to another provider
    Then a notification event of type "failover" is published
    And the notification severity is "warning"

  Scenario: SSE endpoint streams notification events as JSON
    Given a notification event of type "turn_complete" is published
    When a client subscribes to the notification SSE endpoint
    Then the SSE stream emits a JSON payload with keys "ID", "Type", "Severity", "Message", "Provider", "Model"

  Scenario: SSE endpoint ignores non-notification events
    Given a tool execute event is published
    When a client subscribes to the notification SSE endpoint
    Then the SSE stream emits no notification payloads
