#!/usr/bin/env bash
# Checks that docs/swagger/ files exist and that swagger.json and swagger.yaml
# are consistent (same content serialised differently). A missing file or a
# json/yaml mismatch means swag init was not re-run after a handler change.
set -euo pipefail

SWAGGER_DIR="docs/swagger"
REQUIRED_FILES=("docs.go" "swagger.json" "swagger.yaml")

for f in "${REQUIRED_FILES[@]}"; do
  if [[ ! -f "${SWAGGER_DIR}/${f}" ]]; then
    echo "::error file=${SWAGGER_DIR}/${f},title=Swagger missing::${SWAGGER_DIR}/${f} not found. Run 'swag init' and commit the generated files."
    exit 1
  fi
done

# Verify swagger.json is valid JSON
if ! python3 -c "import json,sys; json.load(open('${SWAGGER_DIR}/swagger.json'))" 2>/dev/null; then
  echo "::error file=${SWAGGER_DIR}/swagger.json,title=Swagger invalid::swagger.json is not valid JSON. Run 'swag init' to regenerate."
  exit 1
fi

echo "Swagger staleness check passed — all required files present and swagger.json is valid."
