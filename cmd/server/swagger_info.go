// Package main — global Swagger annotations. `swag init` reads this file
// (via `-g swagger_info.go`) to build the top-level OpenAPI spec.
// Per-endpoint annotations live in internal/adapter/inbound/http/handlers.go.
//
// Regenerate the spec with:
//
//	make swag
//
// The generated files under docs/swagger/ are checked into the repo.
// CI's swag-check fails a PR whose annotations diverge from them.
//
// @title           Signup BFF API
// @version         0.1.0
// @description     Public trial-signup BFF for the XpertPMS platform.
// @description
// @description     **Unauthenticated.** All routes are public — no JWT or tenant identity at signup time.
// @description     Session identity is established by SB-1 (cookie + CSRF token); every state-changing route requires both.
// @description
// @description     **Flow.** SB-1 → SB-2 → (if 202) poll SB-3 → (if 409 slug_taken) SB-4.
// @description
// @description     **Rate limits.** 3 signups per IP per hour; 5 attempts per email per 24 hours; 20 sessions per IP per hour; 60 status polls per session per minute.
//
// @contact.name   BCBP Solutions — Web / Growth
// @contact.email  sharmila.dayalan@bcbpsolutions.com
//
// @license.name   Proprietary
//
// @host      signup.bcbpsolutions.com
// @BasePath  /api/v1/signup
//
// @securityDefinitions.apikey SessionCookie
// @in                         cookie
// @name                       sbff_sid
// @description                Session cookie set by SB-1 (GET /session). HttpOnly; Secure; SameSite=Strict; 30-minute sliding TTL.
//
// @securityDefinitions.apikey CSRFToken
// @in                         header
// @name                       X-CSRF-Token
// @description                CSRF token returned by SB-1 (GET /session). Required on all state-changing routes (POST). Must match the hash stored in the session cookie.
//
// @tag.name         session
// @tag.description  Session management — start a signup session (SB-1)
//
// @tag.name         signup
// @tag.description  Trial signup — submit the form, poll status, and resolve slug conflicts (SB-2, SB-3, SB-4)
//
// @tag.name         infra
// @tag.description  Health and readiness probes (unauthenticated)
package main
