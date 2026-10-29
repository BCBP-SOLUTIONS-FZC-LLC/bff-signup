# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 0.x     | ✅ Active (pre-release) |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Email: vijay@bcbpsolutions.com
Subject: `[bff-signup] Security vulnerability`

Include in your report:
- Description of the vulnerability and the affected component
- Steps to reproduce
- Potential impact
- Suggested fix or patch (if any)

### Response timeline

| Step | Target |
|------|--------|
| Initial acknowledgement | 48 hours |
| Severity assessment | 5 business days |
| Patch release (critical/high) | 14 days |
| Public disclosure | After patch ships |

## Scope

Areas of particular sensitivity in this service:

- **Password handling** — the plain password must never leave process memory except as a PBKDF2-SHA512 hash sent to RP-1. Any logging, Valkey storage, or trace attribute leak of the plain password or `hashed_password_seed` is a critical vulnerability (SB-INV-2).

- **CSRF protection** — state-changing routes (SB-2, SB-4) require both the session cookie and `X-CSRF-Token` header, validated in constant time. A bypass allows any website to submit trial signups on behalf of a user.

- **Session isolation** — SB-3 and SB-4 reject any `signup_id` that doesn't belong to the caller's session. A bypass could allow an attacker to monitor or interfere with another user's signup.

- **Rate limiting** — per-IP and per-email counters prevent mass account creation and email enumeration. A bypass allows bots to create bulk fake tenants or enumerate registered emails.

- **Email HMAC** — the Valkey rate-limit key for email is `HMAC-SHA256(email, SBFF_HMAC_KEY)`. If `SBFF_HMAC_KEY` leaks, attackers can pre-compute rate-limit keys and bypass per-email limits.

- **CAPTCHA secret** — `SBFF_TURNSTILE_SECRET` is the server-side Turnstile secret. Leaking it allows attackers to forge CAPTCHA tokens and bypass the anti-bot gate entirely.

- **PBKDF2 / Keycloak alignment** — the BFF's password policy check is the only enforcement at signup (Keycloak cannot re-check a pre-hashed credential). If `PBKDF2_ITERATIONS` is set below 210,000, or the hash parameters diverge from the Keycloak trial realm policy, users may be created in Keycloak but unable to log in, or the hash may be cryptographically weak.

- **RP-1 / O&M admission** — the Istio `AuthorizationPolicy` and NetworkPolicy restrict which pods can call IAM internal routes. A misconfiguration could allow any pod in the cluster to provision tenants or check trial eligibility without going through the BFF's abuse controls.
