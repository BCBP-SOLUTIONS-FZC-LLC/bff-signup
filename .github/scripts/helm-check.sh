#!/usr/bin/env bash
# Helm lint + template render + optional kubeconform schema validation.
# Mirrors the iam-org-membership helm-check pattern.
set -euo pipefail

CHART_DIR="deploy/helm"
KUBECONFORM_VERSION="v0.6.7"

echo "::group::Helm lint"
helm lint "${CHART_DIR}" -f "${CHART_DIR}/values-dev.yaml"
echo "::endgroup::"

echo "::group::Helm template (dev)"
helm template bff-signup-dev "${CHART_DIR}" \
  -f "${CHART_DIR}/values-dev.yaml" \
  --set "existingSecret=bff-signup-dev-secrets" \
  > /tmp/rendered-dev.yaml
echo "Rendered $(wc -l < /tmp/rendered-dev.yaml) lines"
echo "::endgroup::"

echo "::group::Helm template (prod)"
helm template bff-signup-prod "${CHART_DIR}" \
  -f "${CHART_DIR}/values-prod.yaml" \
  --set "existingSecret=bff-signup-prod-secrets" \
  > /tmp/rendered-prod.yaml
echo "Rendered $(wc -l < /tmp/rendered-prod.yaml) lines"
echo "::endgroup::"

# kubeconform if available
if command -v kubeconform &>/dev/null; then
  echo "::group::kubeconform (dev)"
  kubeconform -strict -summary /tmp/rendered-dev.yaml
  echo "::endgroup::"
  echo "::group::kubeconform (prod)"
  kubeconform -strict -summary /tmp/rendered-prod.yaml
  echo "::endgroup::"
else
  echo "kubeconform not found — skipping schema validation (install with 'go install github.com/yannh/kubeconform/cmd/kubeconform@${KUBECONFORM_VERSION}')"
fi

echo "Helm check passed."
