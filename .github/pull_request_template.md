## Description
Provide a clear description of the changes.

---

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Refactor
- [ ] Documentation
- [ ] Test
- [ ] Breaking change
- [ ] Security fix
- [ ] Helm / deployment config change

---

## Testing
- [ ] Unit tests added/updated (`make test-unit`)
- [ ] Integration tests added/updated — real Valkey (`make test-integration`)
- [ ] Contract tests updated (`make test-contract`)
- [ ] All tests passing with race detector (`make race`)
- [ ] Manual testing performed

---

## Checklist

### Code Quality
- [ ] Code is properly formatted (`make fmt-check`)
- [ ] Linting passed (`make lint`)
- [ ] Vet passed (`make vet`)
- [ ] Architecture lint passed (`make arch-lint` — no adapter → core imports)
- [ ] No debug logs or commented-out code
- [ ] No secrets, tokens, or credentials hardcoded

### Security (complete for any change touching these areas)
- [ ] **Password handling** — the plain password is never logged, stored in Valkey, or sent anywhere except RP-1 as a PBKDF2 hash (SB-INV-2). Verified the redaction test still passes.
- [ ] **PBKDF2 parameters** — if `PBKDF2_ITERATIONS` or salt length changed, IAM must confirm the Keycloak trial realm hash policy matches. Co-review from `@iam-team` required.
- [ ] **CSRF** — POST routes still require `X-CSRF-Token` header and `Origin` validation. No new POST route bypasses `ValidateOrigin`.
- [ ] **Rate limits** — counters still increment after CAPTCHA (SB-INV-5). No path moves the CAPTCHA check after the rate-limit check.
- [ ] **Email HMAC** — raw email addresses are not stored in Valkey or logs. Only the HMAC (keyed with `SBFF_HMAC_KEY`) is stored.
- [ ] **plan field** — RP-1 and I-1 never receive `"plan": "trial"`. Plan list and fallback contain only `starter`/`pro`/`enterprise` (SB-INV-8).
- [ ] **tenant_id stability** — one `tenant_id` is allocated per signup attempt and reused for every retry/resume/slug change (SB-INV-3). No path allocates a new `tenant_id` after `allocated` state on a same-details resubmit.

### Outbound Contract Changes
*Complete only if `internal/core/port/realm_provisioner.go`, `internal/core/port/org_membership.go`, or any outbound adapter changed.*

- [ ] Contract test updated to assert the new request/response shape
- [ ] Confirmed with IAM team that RP-1 / O&M / eligibility accepts the new shape
- [ ] Error code branch in adapter still uses `errorCode` (UPPER_SNAKE_CASE RFC 9457), never `detail`

### Handoff State Machine Changes
*Complete only if `internal/core/domain/handoff.go` or `internal/core/service/handoff_orchestrator.go` changed.*

- [ ] State machine diagram in LLD §9.1 still matches the code
- [ ] All allowed and rejected transitions are covered by unit tests
- [ ] `IsTerminal()`, `CanResume()`, `CanChangeSlug()` updated if new states added

### Helm / Config Changes
- [ ] New required env vars added to `config.go` `Validate()` and to `.env-example`
- [ ] New env vars documented in `deploy/helm/values.yaml` and `docs/lld/signup-bff-lld.md §12`
- [ ] NetworkPolicy egress rules updated if a new external endpoint is called

### Documentation
- [ ] LLD updated if a contract, flow, or invariant changed (`docs/lld/signup-bff-lld.md`)
- [ ] `.claude/` docs updated if key-files, flows, or operations changed

---

## Related Issue
Closes #<issue-id>

---

## Deployment Notes
Mention anything important for operators: new required env vars, Helm value changes, IAM-side prerequisite, Valkey key format changes.
