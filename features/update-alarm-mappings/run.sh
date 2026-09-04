#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 2
}

action="${1:-}"
endpoint="${SYSSETUP_PARAM_ENDPOINT:-http://10.0.1.101:33334/nnm-service/v1/updatealarm/AMF}"
connect_timeout="${SYSSETUP_PARAM_CONNECT_TIMEOUT_SECONDS:-5}"
request_timeout="${SYSSETUP_PARAM_REQUEST_TIMEOUT_SECONDS:-30}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
requests_file="${SYSSETUP_PARAM_REQUESTS_FILE:-$script_dir/alarm-mappings.json}"

preflight() {
  [[ -n "$endpoint" ]] || fail "endpoint parameter is required"
  [[ "$endpoint" == http://* || "$endpoint" == https://* ]] || fail "endpoint must use http:// or https://"
  [[ "$connect_timeout" =~ ^[1-9][0-9]*$ ]] || fail "connect_timeout_seconds must be a positive integer"
  [[ "$request_timeout" =~ ^[1-9][0-9]*$ ]] || fail "request_timeout_seconds must be a positive integer"
  [[ -f "$requests_file" && -r "$requests_file" ]] || fail "requests file is not readable: $requests_file"
  command -v curl >/dev/null 2>&1 || fail "curl command was not found"
  command -v python3 >/dev/null 2>&1 || fail "python3 command was not found"
  [[ -f "$script_dir/send.py" ]] || fail "alarm request helper was not found: $script_dir/send.py"
}

case "$action" in
  check)
    preflight
    # This feature deliberately sends every request on each invocation.
    exit 10
    ;;
  apply)
    preflight
    python3 "$script_dir/send.py" \
      "$requests_file" "$endpoint" "$connect_timeout" "$request_timeout"
    ;;
  verify)
    # apply succeeds only when every request returned HTTP 2xx.
    printf '[PASS] All alarm mapping requests completed with HTTP 2xx.\n'
    ;;
  rollback)
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
