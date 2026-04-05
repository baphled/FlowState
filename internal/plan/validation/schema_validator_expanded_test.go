package validation_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/validation"
)

var _ = Describe("SchemaValidator Expanded Fields", func() {
	var validator *validation.SchemaValidator

	BeforeEach(func() {
		validator = &validation.SchemaValidator{}
	})

	Context("when plan has all expanded fields", func() {
		It("passes validation with full score", func() {
			planText := `---
id: test-001
title: Test Plan
description: A test plan
status: draft
created_at: 2026-01-01T00:00:00Z
tldr: This is the TLDR summary
context:
  original_request: Build a REST API
  interview_summary: User wants API for tasks
  research_findings: Found relevant patterns
work_objectives:
  core_objective: Create a working API
  deliverables:
    - Endpoint GET /tasks
    - Endpoint POST /tasks
  definition_of_done:
    - All tests pass
    - Documentation complete
  must_have:
    - Authentication
  must_not_have:
    - Public access
verification_strategy: Run integration tests
---

## TL;DR
This is the TLDR summary

## Context

### Original Request
Build a REST API

### Interview Summary
User wants API for tasks

### Research Findings
Found relevant patterns

## Work Objectives

### Core Objective
Create a working API

### Deliverables
- Endpoint GET /tasks
- Endpoint POST /tasks

### Definition of Done
- All tests pass
- Documentation complete

### Must Have
- Authentication

### Must Not Have
- Public access

## Verification Strategy
Run integration tests

## Tasks

### Task 1
Description: Create the GET endpoint
`
			result, err := validator.Validate(planText)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
			Expect(result.Score).To(Equal(1.0))
		})
	})

	Context("when plan has expanded fields as warnings (backward compat)", func() {
		It("passes validation with empty expanded fields", func() {
			planText := `---
id: test-002
title: Minimal Plan
description: A minimal plan
status: draft
created_at: 2026-01-01T00:00:00Z
---

## Tasks

### Task 1
Description: Do something
`
			result, err := validator.Validate(planText)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
		})
	})

	Context("when plan is missing required expanded fields", func() {
		It("adds warning for missing TLDR", func() {
			planText := `---
id: test-003
title: Plan Without TLDR
description: A plan without TLDR
status: draft
created_at: 2026-01-01T00:00:00Z
context:
  original_request: Build something
work_objectives:
  core_objective: Do the thing
  deliverables:
    - Task 1
verification_strategy: Test it
---

## Tasks

### Task 1
Description: Do something
`
			result, err := validator.Validate(planText)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
			Expect(result.Warnings).To(ContainElement(ContainSubstring("TL;DR")))
		})

		It("adds warning when Context has no OriginalRequest", func() {
			planText := `---
id: test-004
title: Plan Without OriginalRequest
description: A plan without original request
status: draft
created_at: 2026-01-01T00:00:00Z
tldr: Test TLDR
work_objectives:
  core_objective: Do the thing
  deliverables:
    - Task 1
verification_strategy: Test it
---

## Tasks

### Task 1
Description: Do something
`
			result, err := validator.Validate(planText)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
			Expect(result.Warnings).To(ContainElement(ContainSubstring("OriginalRequest")))
		})

		It("adds warning when WorkObjectives has no CoreObjective", func() {
			planText := `---
id: test-005
title: Plan Without CoreObjective
description: A plan without core objective
status: draft
created_at: 2026-01-01T00:00:00Z
tldr: Test TLDR
context:
  original_request: Build something
verification_strategy: Test it
---

## Tasks

### Task 1
Description: Do something
`
			result, err := validator.Validate(planText)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
			Expect(result.Warnings).To(ContainElement(ContainSubstring("CoreObjective")))
		})
	})
})
