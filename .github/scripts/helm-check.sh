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
  --set "ingress.enabled=true" --set "ingress.gateway.name=signup-gateway" \
  --set "existingSecret=bff-signup-prod-secrets" \
  > /tmp/rendered-prod.yaml
echo "Rendered $(wc -l < /tmp/rendered-prod.yaml) lines"
echo "::endgroup::"

# kubeconform if available. CRDs (PrometheusRule, ServiceMonitor, Istio
# AuthorizationPolicy/ServiceEntry, Gateway API HTTPRoute) are validated against
# the community CRD schema catalog; without it kubeconform cannot find their
# schemas and fails.
CRD_SCHEMAS='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
if command -v kubeconform &>/dev/null; then
  for env in dev prod; do
    echo "::group::kubeconform (${env})"
    kubeconform -strict -summary -schema-location default -schema-location "${CRD_SCHEMAS}" "/tmp/rendered-${env}.yaml"
    echo "::endgroup::"
  done
else
  echo "kubeconform not found — skipping schema validation (install with 'go install github.com/yannh/kubeconform/cmd/kubeconform@${KUBECONFORM_VERSION}')"
fi

echo "Helm check passed."
