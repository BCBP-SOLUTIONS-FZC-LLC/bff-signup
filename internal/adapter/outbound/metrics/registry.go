package metrics

// RegistryStatus mirrors iam-org-membership's registry.go — the checked-in
// source of truth for the Platform Observability Registry (rules 9-12 of the
// Enterprise Platform Observability Standard: shared names/labels are part of
// the observability contract; a service SHALL NOT invent or adopt one
// unilaterally).
//
// Ratification checklist (for the reviewing body, once one exists):
//   - Semantic definition holds identically across at least two domains.
//   - Label vocabulary is acceptable as canonical.
//   - No approved label is high-cardinality (user_id/email/tenant_id/
//     request_id/event_id/session_id or equivalent unbounded value).
//   - Aggregation expectations validated across at least one other service.
//   - Once approved: flip Status to StatusCanonical, follow the standard's
//     Backward Compatibility migration order, deprecate the Tier 3 predecessor.
type RegistryStatus string

const (
	// StatusCanonical marks a metric already ratified by the registry.
	StatusCanonical RegistryStatus = "canonical"

	// StatusProposed marks a metric submitted for ratification but not yet
	// approved. Per rule 12, Proposed metrics MUST be shadow-emitted only —
	// no alert, dashboard, recording rule, SLO, or HPA reference may treat
	// them as authoritative until Status flips to Canonical.
	StatusProposed RegistryStatus = "proposed"
)

// RegistryEntry carries the exact information the standard's Registry
// Ratification Requirement calls for (semantic definition, required labels,
// allowed label values, cardinality justification, aggregation expectations).
type RegistryEntry struct {
	Name               string
	Tier               string // "platform" (Tier 1) or "domain" (Tier 2)
	Status             RegistryStatus
	SemanticDefinition string
	RequiredLabels     []string
	ApprovedLabels     []string
	LabelValues        map[string][]string
	Cardinality        string
	AggregationNotes   string
	SupersedesTier3    []string
}

// PlatformRegistry lists every platform_*/domain-shared metric this service
// writes. Their names, help texts, label sets and buckets are owned by the
// central Platform Observability Registry and built via gincommon.NewShared*Vec.
// Proposed entries are not yet wired into this repo's alerts, recording rules,
// SLOs, or HPA references; their Tier-3 counterparts stay authoritative until
// real observability governance approves them.
//
// The bff domain has only one service currently (bff-signup), so no Tier 2
// bff_* metrics are defined. Should a second BFF service emerge (e.g.
// bff-onboarding), common concepts can be promoted to bff_* at that time.
var PlatformRegistry = []RegistryEntry{
	{
		Name:               "platform_dependency_request_seconds",
		Tier:               "platform",
		Status:             StatusProposed,
		SemanticDefinition: "Client-observed wall time of one call to an external dependency, by outcome. Mirrors the iam-org-membership submission (registry.go). This service records synchronous REST calls to Realm Provisioner (RP-1), Org & Membership (I-1, eligibility, PUB-1), Turnstile (CAPTCHA), the Catalog Service and the Billing Service (SGN-2).",
		RequiredLabels:     []string{"domain", "service", "environment"},
		ApprovedLabels:     []string{"dependency", "operation", "outcome"},
		LabelValues: map[string][]string{
			"dependency": {"realm_provisioner", "org_membership", "turnstile", "catalog", "billing"},
			"operation":  {"provision_trial", "provision_direct", "provision_tenant", "trial_eligibility", "slug_resolve", "siteverify", "plans", "authorize_and_charge"}, // RP-1, RP-2, I-1, CR-4, PUB-1, Turnstile, Catalog, Billing B-I1
			"outcome":    {"success", "error"},                                                                                                                            // error = transport failure or 5xx
		},
		Cardinality:      "Bounded — 5 peers × 8 operations in total × 2 outcomes; no request/session/tenant identifier is ever a label value.",
		AggregationNotes: "Error ratio per dependency: sum by (domain,service,environment,dependency) (rate(platform_dependency_request_seconds_count{outcome=\"error\"}[w])) / sum by (domain,service,environment,dependency) (rate(platform_dependency_request_seconds_count[w])). p99: histogram_quantile(0.99, sum by (le,domain,service,environment,dependency) (rate(platform_dependency_request_seconds_bucket[w]))).",
		SupersedesTier3:  []string{"bff_signup_dependency_call_duration_seconds"},
	},
}
