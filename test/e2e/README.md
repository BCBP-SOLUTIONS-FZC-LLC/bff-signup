# End-to-end tests (bff-signup)

Real HTTP requests against a running service, with real Valkey and the two
mock servers (O&M :9002, Realm Provisioner :9003). Build tag: `e2e`.

## Run

1. `make docker-up`                       (Valkey; Docker Desktop must be running)
2. `~/BCBP-IAM/bff/mocks/run_all.sh`      (O&M and RP mocks)
3. `make run`                             (needs `CAPTCHA_SKIP=true`, `APP_ENV=dev` in `.env`)
4. `make test-e2e`                        (in another terminal)

Optional: `E2E_VALKEY_CONTAINER=<name> make test-e2e` also runs the "Valkey down" test (SB3-14).
Optional: `E2E_LOG_FILE=<service log>` runs the log checks (start the service with `make run 2>&1 | tee ~/bff-run.log`).
Other settings: `E2E_BASE_URL`, `E2E_OM_URL`, `E2E_RP_URL`, `E2E_METRICS_URL` (default `http://127.0.0.1:9090`), `E2E_ORIGIN` (must be `https://` + `PUBLIC_HOST` from `.env`).

The mocks must support `GET/DELETE /__mock/requests` (they record the headers and body of each call so tests can check what the service sent). Use the current mock files.

## Notes

- Each test uses a random client IP, e-mail and slug, so tests do not share rate-limit budget.
- Stage 1 (`gap_fixes_test.go`) covers the gaps fixed on `test/gap-fix`; SB2-66, S-05 and O-03 are
  covered by unit tests (see the header of that file).
- Stage 2 automates the manually tested cases, one file per area:
  `session_and_validation_test.go` (SB-1, SB2-01..09), `trial_fields_test.go` (SB2-10..40),
  `handoff_status_test.go` (SB2 handoff/resubmit, SB-3), `slug_and_paid_test.go` (SB-4, SB-5),
  `ops_security_test.go` (health, metrics, headers, logs), `inputs_and_downstream_test.go`
  (input boundaries, downstream headers/bodies, locale, SB-2 metrics).
- Each test name starts with the case ID from the test workbook (for example `TestSB2_47_...`).
- Stay manual (cannot be automated here): Valkey stopped (SB1-03, SB2-41, H-03, H-04), real Turnstile
  (SB2-32/33/36/42, M-07/M-08, SB2-RL01), timing (SB2-37a/b), the `identity_created` state (SB2-52a/53),
  15-minute wait (SB3-05), unreachable (SB3-13), SB5-02 (needs `PAID_SIGNUP_ENABLED=true`),
  SB4-08/13, SB2-RP01 (OBS-41), O-RED05, S-03, X-01/X-02.
- Known gap documented in `handoff_status_test.go` (SB2-43/44): the service does not retry RP-1 or
  eligibility once on 502/503 as LLD §17.2 says (OBS-55).
