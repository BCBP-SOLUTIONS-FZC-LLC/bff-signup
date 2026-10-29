#!/usr/bin/env bash
# Logs the pushed image digest and tags to the GitHub Step Summary.
set -euo pipefail

{
  echo "### Image pushed to GHCR"
  echo ""
  echo "| Field | Value |"
  echo "|---|---|"
  echo "| Digest | \`${IMAGE_DIGEST}\` |"
  echo "| SHA tag | \`sha-${GITHUB_SHA::7}\` |"
  echo ""
  echo "**Tags:**"
  echo "\`\`\`"
  echo "${IMAGE_TAGS}"
  echo "\`\`\`"
} >> "$GITHUB_STEP_SUMMARY"

echo "Image digest: ${IMAGE_DIGEST}"
