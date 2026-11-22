package registry

import "os"

// zaiPlan resolves the z.ai plan: ZAI_PLAN, else coding.
func zaiPlan() string {
	plan := os.Getenv("ZAI_PLAN")
	if plan == "" {
		// Live evidence 2026-11-04 (chain meta-70faf6fe0e1f): a
		// coding-plan subscription key 429s (code 1113) on every
		// call to the general pay-per-token endpoint, while the
		// coding endpoint serves it 200. An unset plan must
		// therefore default to the coding route — the silent
		// general-endpoint fallback breaks every coding-plan key.
		// Operators on pay-per-token keys opt out explicitly with
		// ZAI_PLAN=general.
		plan = "coding"
	}
	return plan
}
