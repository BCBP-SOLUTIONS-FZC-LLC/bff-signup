package domain

// Plan is a tenant plan tier. Only these three values are accepted by RP-1 and I-1.
type Plan string

const (
	PlanStarter    Plan = "starter"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

// ValidPlan reports whether p is one of the three accepted plan tiers.
func ValidPlan(p Plan) bool {
	switch p {
	case PlanStarter, PlanPro, PlanEnterprise:
		return true
	}
	return false
}

// PlanInfo is the plan catalogue entry returned to the browser in SB-1.
type PlanInfo struct {
	Code      Plan   `json:"code"`
	Name      string `json:"name"`
	TrialDays int    `json:"trial_days"`
}
