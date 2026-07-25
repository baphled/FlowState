@failover
Feature: Auth failure triggers reactive OAuth refresh

  When a provider that implements RefreshCapable returns an auth failure
  (HTTP 401), the failover hook should attempt a reactive token refresh
  before applying cooldown. If the refresh succeeds and the retry succeeds,
  the provider/model pair is NOT marked for cooldown.

  If the refresh fails or the retry after refresh also fails, the standard
  cooldown is applied as before.

  Scenario: 401 triggers refresh that succeeds and retry succeeds
    Given a failover hook with a single candidate "openai" / "gpt-4o"
    And the candidate fails once with auth failure then succeeds
    When the failover hook executes a chat request
    Then the health manager does NOT mark "openai" / "gpt-4o" as rate-limited
