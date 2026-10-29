---
name: Bug report
about: Report a defect in the bff-signup service (API, CAPTCHA, rate limiting, Valkey, provisioning handoff)
title: '[BUG] '
labels: bug
assignees: ''
---

## Description
A clear description of the bug.

## Service version
`github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup` — tag / commit SHA:

## Go version
`go version goX.Y.Z ...`

## Environment
- [ ] Local dev (`make run`)
- [ ] Docker Compose (`make docker-up`)
- [ ] Staging
- [ ] Production

## Affected area
- [ ] SB-1 `GET /session`
- [ ] SB-2 `POST /trial`
- [ ] SB-3 `GET /{signup_id}/status`
- [ ] SB-4 `POST /{signup_id}/slug`
- [ ] CAPTCHA verification (Turnstile)
- [ ] Rate limiting
- [ ] CSRF protection
- [ ] Valkey / session / handoff store
- [ ] RP-1 provisioning (Realm Provisioner)
- [ ] I-1 provisioning (Org & Membership)
- [ ] Trial eligibility check
- [ ] Password hashing (PBKDF2)
- [ ] Handoff state machine

## Steps to reproduce
1.
2.
3.

## Expected behaviour
What you expected to happen.

## Actual behaviour
What actually happened. Include error codes, HTTP status, log output, or `signup_id`.

```
// paste relevant log output or error here
```

## Minimal reproduction
```bash
# paste the smallest curl command that triggers the bug
```

## Additional context
Handoff state at time of failure, Valkey key values (without secrets), related `signup_id` or `trace_id`.
