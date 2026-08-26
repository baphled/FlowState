---
id: test-valid-plan
title: Valid Test Plan
description: A complete plan for testing purposes
status: draft
created_at: 2026-01-01T00:00:00Z
---

## Implement User Authentication

Add JWT-based authentication middleware for the REST API.

### Acceptance Criteria
- Middleware validates JWT tokens on protected routes
- Invalid tokens return 401 Unauthorised response
- Expired tokens return 401 with "token expired" message

**Skills**: golang, security, testing
**Category**: feat
**Dependencies**: none
**Estimated Effort**: Moderate
**Wave**: 1

## Add Database Migration System

Set up Goose for managing database schema migrations.

### Acceptance Criteria
- Migrations run automatically on application start
- Failed migrations halt startup with clear error
- Migration state tracked in schema_migrations table

**Skills**: golang, sql, devops
**Category**: feat
**Dependencies**: Implement User Authentication
**Estimated Effort**: Simple
**Wave**: 2
