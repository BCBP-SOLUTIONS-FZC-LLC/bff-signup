#!/usr/bin/env bash
# Image size + startup gate smoke tests for the CI-built Docker image.
set -euo pipefail

echo "::group::Image size check (linux/amd64)"
# bff-signup is a distroless Go binary with no DB driver — limit is 50 MB.
MAX_MB=50
size=$(docker image inspect bff-signup-ci-test --format='{{.Size}}')
mb=$((size / 1024 / 1024))
echo "Image size: ${mb} MB (limit: ${MAX_MB} MB)"
[ "${mb}" -le "${MAX_MB}" ] &
P1=$!
echo "::endgroup::"

echo "::group::Startup gate"
# Server must exit non-zero on missing required env vars (VALKEY_URL etc.),
# proving validateRequiredEnv fires correctly on startup.
exit_code=0
timeout 10s docker run --rm bff-signup-ci-test 2>/dev/null || exit_code=$?
echo "Container exit code: ${exit_code} (expected non-zero)"
[ "${exit_code}" -ne 0 ] &
P2=$!
echo "::endgroup::"

wait $P1 || {
  echo "::error file=Dockerfile,title=Image size::Image is ${mb} MB, exceeds ${MAX_MB} MB limit — check COPY/ADD instructions"
  exit 1
}
wait $P2 || {
  echo "::error file=cmd/server/main.go,title=Startup gate::Server exited 0 on missing env vars — config.Validate() must exit non-zero"
  exit 1
}

{
  echo "### Smoke test results"
  echo "- ✅ Startup gate: server exits ${exit_code} on missing env vars"
  echo "- 📦 Image size (linux/amd64): **${mb} MB** (limit: ${MAX_MB} MB)"
  echo "- 🔍 [Security (Trivy SARIF results)](https://github.com/${GITHUB_REPOSITORY}/security/code-scanning)"
} >> "$GITHUB_STEP_SUMMARY"
