package registry

import (
	"os"

	"github.com/baphled/flowstate/internal/config"
)

// zaiPlan resolves the z.ai plan: ZAI_PLAN, else providers.zai.plan, else
// coding. The default keeps a provider config written before the plan and
// route keys existed working unchanged.
func zaiPlan(cfg *config.ProviderConfig) string {
	plan := os.Getenv("ZAI_PLAN")
	if plan == "" && cfg != nil {
		plan = cfg.Plan
	}
	if plan == "" {
		// Live evidence 2026-11-04 (chain meta-70faf6fe0e1f): a
		// coding-plan subscription key 429s (code 1113) on every
		// call to the general pay-per-token endpoint, while the
		// coding endpoint serves it 200. An unset plan must
		// therefore default to the coding route — the silent
		// general-endpoint fallback breaks every coding-plan key.
		// Operators on pay-per-token keys opt out explicitly with
		// plan: general.
		plan = "coding"
	}
	return plan
}
