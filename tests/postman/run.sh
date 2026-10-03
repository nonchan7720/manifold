#!/usr/bin/env bash
# Runs the Postman CLI end-to-end suite for tool search, tool filtering and
# tool authorization against a locally built gateway:
#
#   OPA (docker)  <--  manifold gateway  -->  stub Petstore API (Go)
#                           ^
#                     postman collection run
#
# Needs go, docker and the Postman CLI (https://learning.postman.com/docs/postman-cli/).
# Without docker, point OPA_BIN at an opa binary (https://www.openpolicyagent.org/docs/#running-opa)
# and it is started directly instead. Logs, the audit file and the JUnit report
# land in tests/postman/tmp/.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DIR="$ROOT/tests/postman"
TMP="$DIR/tmp"
OPA_IMAGE="${OPA_IMAGE:-openpolicyagent/opa:1.19.1}"
OPA_CONTAINER="${OPA_CONTAINER:-manifold-postman-opa}"
STUB_ADDR="127.0.0.1:18080"
GATEWAY_URL="http://127.0.0.1:19999"
OPA_URL="http://127.0.0.1:8181"

# The gateway only talks to loopback addresses (the stub, OPA) when it knows it
# runs in a test or CI environment (pkg/internal/env).
export TEST=true
export AUDIT_OUTPUT="$TMP/audit.jsonl"

required=(go curl postman)
if [ -z "${OPA_BIN:-}" ]; then
  required+=(docker)
fi
for cmd in "${required[@]}"; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "error: $cmd is required (set OPA_BIN to run OPA without docker)" >&2
    exit 1
  fi
done

mkdir -p "$TMP"
rm -f "$AUDIT_OUTPUT" "$TMP"/*.log "$TMP"/junit.xml

STUB_PID=""
GW_PID=""
OPA_PID=""
cleanup() {
  [ -n "$GW_PID" ] && kill "$GW_PID" 2>/dev/null || true
  [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null || true
  [ -n "$OPA_PID" ] && kill "$OPA_PID" 2>/dev/null || true
  if [ -z "${OPA_BIN:-}" ]; then
    docker rm -f "$OPA_CONTAINER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

wait_for() {
  local url="$1" name="$2"
  for _ in $(seq 1 60); do
    if curl -sf -o /dev/null "$url"; then
      return 0
    fi
    sleep 0.5
  done
  echo "error: $name did not become ready at $url" >&2
  return 1
}

echo "==> building manifold and the stub API"
go build -o "$TMP/manifold" "$ROOT"
go build -o "$TMP/stub" "$ROOT/tests/postman/stub"

# The OPA bundle: the policy shared with examples/opa plus this suite's groups.
rm -rf "$TMP/opa-bundle"
mkdir -p "$TMP/opa-bundle"
cp "$ROOT/examples/opa/policy.rego" "$DIR/opa/data.json" "$TMP/opa-bundle/"
if [ -n "${OPA_BIN:-}" ]; then
  echo "==> starting OPA ($OPA_BIN)"
  "$OPA_BIN" run --server --addr=127.0.0.1:8181 -b "$TMP/opa-bundle" >"$TMP/opa.log" 2>&1 &
  OPA_PID=$!
else
  echo "==> starting OPA ($OPA_IMAGE)"
  docker rm -f "$OPA_CONTAINER" >/dev/null 2>&1 || true
  docker run -d --rm --name "$OPA_CONTAINER" -p 127.0.0.1:8181:8181 \
    -v "$TMP/opa-bundle:/policies:ro" \
    "$OPA_IMAGE" run --server --addr=:8181 -b /policies >/dev/null
fi
wait_for "$OPA_URL/health" "OPA"

echo "==> starting the stub API on $STUB_ADDR"
"$TMP/stub" -addr "$STUB_ADDR" -spec "$ROOT/pkg/internal/mcpsrv/fixtures/petstore_oas.json" \
  >"$TMP/stub.log" 2>&1 &
STUB_PID=$!
wait_for "http://$STUB_ADDR/openapi.json" "stub API"

echo "==> starting the gateway on $GATEWAY_URL"
(cd "$DIR" && exec "$TMP/manifold" gateway -c config) >"$TMP/gateway.log" 2>&1 &
GW_PID=$!
wait_for "$GATEWAY_URL/healthz" "gateway"

echo "==> running the Postman collection"
postman collection run "$DIR/manifold-tool-search.postman_collection.json" \
  --env-var "baseUrl=$GATEWAY_URL" \
  -r cli,junit --reporter-junit-export "$TMP/junit.xml"

echo "==> checking the audit log"
audit_has() {
  if ! grep -q -- "$1" "$AUDIT_OUTPUT"; then
    echo "error: audit log has no line matching $1" >&2
    cat "$AUDIT_OUTPUT" >&2
    exit 1
  fi
}
audit_has '"tool":"tool_search".*"outcome":"success"'
audit_has '"tool":"get_pet".*"outcome":"success"'
audit_has '"tool":"deletepet".*"outcome":"denied"'
audit_has '"tool":"tool_search".*"outcome":"denied"'

echo "==> OK"
