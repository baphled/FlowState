Feature: Blocking question tool
  As a user
  I want agents to be able to ask me a clarifying question and wait for my answer
  So that the agent can proceed with information I actually chose

  Scenario: Question tool blocks until the user answers
    Given a question registry is available
    And a question request registry is wired to a question tool
    When the agent asks "Which database should I use?" with options "postgres" and "sqlite"
    Then a pending question request should be registered for the session
    And the question tool should still be waiting

  Scenario: Answering resumes the blocked tool call
    Given a question registry is available
    And a question request registry is wired to a question tool
    And the agent asks "Which database should I use?" with options "postgres" and "sqlite"
    When the user answers the pending question with "postgres"
    Then the question tool should return the answer "postgres"

  Scenario: Unanswered question times out
    Given a question registry is available with a short timeout
    And a question request registry is wired to a question tool
    And the agent asks "Which database should I use?" with options "postgres" and "sqlite"
    When the question timeout elapses without an answer
    Then the question tool should return a timeout error

  Scenario: Question request surfaces on the session pending list
    Given a question registry is available
    And a question request registry is wired to a question tool
    And the agent asks "Which database should I use?" with options "postgres" and "sqlite"
    Then the session should list one pending question
    When the user answers the pending question with "postgres"
    Then the session should list no pending questions

  Scenario: Missing question argument is rejected
    Given a question registry is available
    And a question request registry is wired to a question tool
    When the agent asks a question without a question argument
    Then the question tool should return an argument error without registering a request
