# End-to-end tests (bff-signup)

Real HTTP requests against a running service, with real Valkey and the two
mock servers (O&M :9002, Realm Provisioner :9003). Build tag: `e2e`.

## Run

1. `make docker-up`                       (Valkey; Docker Desktop must be running)
2. `~/BCBP-IAM/bff/mocks/run_all.sh`      (O&M and RP mocks)
3. `make run`                             (needs `CAPTCHA_SKIP=true`, `APP_ENV=dev` in `.env`)
4. `make test-e2e`                        (in another terminal)

Optional: `E2E_VALKEY_CONTAINER=<name> make test-e2e` also runs the "Valkey down" test (SB3-14).
Other settings: `E2E_BASE_URL`, `E2E_OM_URL`, `E2E_RP_URL`, `E2E_ORIGIN` (must be `https://` + `PUBLIC_HOST` from `.env`).

## Notes

- Each test uses a random client IP, e-mail and slug, so tests do not share rate-limit budget.
- Stage 1 covers the gaps fixed on `test/gap-fix`; SB2-66, S-05 and O-03 are covered by unit tests
  (see the header of `gap_fixes_test.go`).
