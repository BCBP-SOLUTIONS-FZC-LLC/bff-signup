---
name: Feature request
about: Propose a new endpoint, abuse control, provisioning flow change, or config option
title: '[FEAT] '
labels: enhancement
assignees: ''
---

## Problem / motivation
What problem does this solve? Which users or flows are affected?

## Proposed solution
Describe the API or behaviour change you'd like.

```bash
# Example: new endpoint or changed request shape
```

## Affected areas
- [ ] New public endpoint
- [ ] Handoff state machine (new state or transition)
- [ ] Outbound contract (RP-1, I-1, or eligibility request/response shape)
- [ ] Abuse controls (rate limits, CAPTCHA, CSRF)
- [ ] Password policy / PBKDF2 parameters
- [ ] Valkey key catalogue (new key type or TTL change)
- [ ] Configuration (new env var)
- [ ] Helm / deployment config
- [ ] Phase 1.5 (direct-paid signup)

## IAM contract impact
If this changes what the BFF sends to RP-1, I-1, or the eligibility endpoint, describe the IAM team work required.

## Security considerations
Does this change affect CAPTCHA, CSRF, rate limiting, session isolation, or password handling?

## Alternatives considered
Other approaches you evaluated and why you ruled them out.

## Acceptance criteria
- [ ]
- [ ]
- [ ]

## Additional context
Links to related LLD sections, TL decisions, or IAM service issues.
