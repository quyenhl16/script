#!/usr/bin/env bash
set -uo pipefail

action="${1:-}"
amf_query_url="${SYSSETUP_PARAM_AMF_QUERY_URL:-http://68.240.39.17:20000/nnrf-disc/v1/nf-instances?guami=%7B%22plmnId%22%3A%7B%22mcc%22%3A%22452%22%2C%22mnc%22%3A%2210%22%7D%2C%22amfId%22%3A%22010081%22%7D&requester-nf-type=SMF&service-names=namf-comm&target-nf-type=AMF}"
smf_query_url="${SYSSETUP_PARAM_SMF_QUERY_URL:-http://68.240.39.17:20000/nnrf-disc/v1/nf-instances?requester-nf-type=AMF&service-names=nsmf-pdusession&snssais=%5B%7B%22sst%22%3A1%7D%5D&target-nf-type=SMF&target-plmn-list=%5B%7B%22mcc%22%3A%22452%22%2C%22mnc%22%3A%2210%22%7D%5D&requester-nf-instance-id=87986745-44b8-4837-b7c3-955759cb098b&requester-plmn-list=%5B%7B%22mcc%22%3A%22452%22%2C%22mnc%22%3A%2204%22%7D%5D&dnn=mddqs}"
connect_timeout="${SYSSETUP_PARAM_CONNECT_TIMEOUT_SECONDS:-3}"
max_time="${SYSSETUP_PARAM_MAX_TIME_SECONDS:-15}"
curl_insecure="${SYSSETUP_PARAM_CURL_INSECURE:-false}"
require_registered="${SYSSETUP_PARAM_REQUIRE_REGISTERED:-true}"

parse_arguments() {
  local argument
  for argument in "$@"; do
    case "$argument" in
      amf_query_url=*) amf_query_url="${argument#*=}" ;;
      smf_query_url=*) smf_query_url="${argument#*=}" ;;
      connect_timeout_seconds=*) connect_timeout="${argument#*=}" ;;
      max_time_seconds=*) max_time="${argument#*=}" ;;
      curl_insecure=*) curl_insecure="${argument#*=}" ;;
      require_registered=*) require_registered="${argument#*=}" ;;
      *)
        printf 'Unknown argument: %q\n' "$argument" >&2
        return 2
        ;;
    esac
  done
}

if (($# > 1)); then
  parse_arguments "${@:2}" || exit $?
fi

if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  green=$'\033[0;32m'
  red=$'\033[0;31m'
  yellow=$'\033[1;33m'
  blue=$'\033[0;34m'
  reset=$'\033[0m'
else
  green=''
  red=''
  yellow=''
  blue=''
  reset=''
fi

checks=0
failures=0
temp_files=()

pass() {
  checks=$((checks + 1))
  printf '  %s[PASS]%s %s\n' "$green" "$reset" "$1"
}

fail() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  printf '  %s[FAIL]%s %s\n' "$red" "$reset" "$1" >&2
}

validate_url() {
  local name="$1"
  local url="$2"
  local authority

  if [[ -z "$url" || "$url" == *$'\n'* || "$url" == *$'\r'* || \
        ! "$url" =~ ^https?://[^/[:space:]]+(/[^[:space:]]*)?$ ]]; then
    printf '%s must be a valid single-line HTTP(S) URL\n' "$name" >&2
    return 1
  fi
  authority="${url#*://}"
  authority="${authority%%/*}"
  if [[ "$authority" == *@* ]]; then
    printf 'URL userinfo is not allowed in %s\n' "$name" >&2
    return 1
  fi
}

validate_configuration() {
  validate_url amf_query_url "$amf_query_url" || return 2
  validate_url smf_query_url "$smf_query_url" || return 2
  if [[ ! "$connect_timeout" =~ ^[1-9][0-9]*$ ]]; then
    printf 'connect_timeout_seconds must be a positive integer\n' >&2
    return 2
  fi
  if [[ ! "$max_time" =~ ^[1-9][0-9]*$ ]]; then
    printf 'max_time_seconds must be a positive integer\n' >&2
    return 2
  fi
  if ((10#$max_time < 10#$connect_timeout)); then
    printf 'max_time_seconds must be greater than or equal to connect_timeout_seconds\n' >&2
    return 2
  fi
  case "$curl_insecure" in
    true|false) ;;
    *)
      printf 'curl_insecure must be true or false\n' >&2
      return 2
      ;;
  esac
  case "$require_registered" in
    true|false) ;;
    *)
      printf 'require_registered must be true or false\n' >&2
      return 2
      ;;
  esac
}

preflight() {
  validate_configuration || return
  if ! command -v curl >/dev/null 2>&1; then
    printf 'curl is required but was not found in PATH\n' >&2
    return 1
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    printf 'python3 is required but was not found in PATH\n' >&2
    return 1
  fi
}

cleanup() {
  local file
  for file in "${temp_files[@]}"; do
    [[ -n "$file" ]] && rm -f -- "$file"
  done
}

print_response_excerpt() {
  local response_file="$1"
  if ! python3 -m json.tool "$response_file" 2>/dev/null; then
    python3 - "$response_file" <<'PY'
import pathlib
import sys

text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")
print(text[:2000])
PY
  fi
}

validate_and_print_response() {
  local response_file="$1"
  local expected_nf_type="$2"

  python3 - "$response_file" "$expected_nf_type" "$require_registered" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
expected_type = sys.argv[2].upper()
require_registered = sys.argv[3] == "true"

try:
    payload = json.loads(path.read_text(encoding="utf-8"))
except (OSError, UnicodeError, json.JSONDecodeError) as exc:
    print(f"Invalid JSON response: {exc}", file=sys.stderr)
    raise SystemExit(2)

print(json.dumps(payload, indent=2, ensure_ascii=False))

instances = payload.get("nfInstances")
if not isinstance(instances, list):
    print("Response field nfInstances is missing or is not an array", file=sys.stderr)
    raise SystemExit(3)

matching = [
    item for item in instances
    if isinstance(item, dict) and str(item.get("nfType", "")).upper() == expected_type
]
if not matching:
    print(f"No {expected_type} instance was returned by NRF", file=sys.stderr)
    raise SystemExit(4)

if require_registered and not any(
    str(item.get("nfStatus", "")).upper() == "REGISTERED" for item in matching
):
    print(f"No {expected_type} instance has REGISTERED status", file=sys.stderr)
    raise SystemExit(5)

registered = sum(
    str(item.get("nfStatus", "")).upper() == "REGISTERED" for item in matching
)
print(
    f"Summary: target={expected_type}, matching={len(matching)}, "
    f"registered={registered}, total={len(instances)}"
)
PY
}

query_nrf() {
  local label="$1"
  local expected_nf_type="$2"
  local url="$3"
  local response_file
  local http_code
  local status
  local -a curl_options=(
    --silent
    --show-error
    --connect-timeout "$connect_timeout"
    --max-time "$max_time"
    --header 'Accept: application/json'
  )

  response_file="$(mktemp "${TMPDIR:-/tmp}/syssetup-nrf-query.XXXXXX")" || {
    fail "$label: could not create a temporary response file"
    return
  }
  temp_files+=("$response_file")
  if [[ "$curl_insecure" == true ]]; then
    curl_options+=(--insecure)
  fi

  printf '\n%sQuery: %s%s\n' "$yellow" "$label" "$reset"
  printf 'URL: %s\n\n' "$url"
  http_code="$(curl "${curl_options[@]}" --output "$response_file" \
    --write-out '%{http_code}' "$url")"
  status=$?
  if ((status != 0)); then
    fail "$label: curl failed with exit status $status"
    return
  fi
  if [[ ! "$http_code" =~ ^2[0-9][0-9]$ ]]; then
    print_response_excerpt "$response_file"
    fail "$label: NRF returned HTTP $http_code"
    return
  fi
  if validate_and_print_response "$response_file" "$expected_nf_type"; then
    pass "$label: NRF returned HTTP $http_code with a matching $expected_nf_type instance"
  else
    fail "$label: NRF returned HTTP $http_code but response validation failed"
  fi
}

run_queries() {
  trap cleanup EXIT
  printf '%s=========================================================%s\n' "$blue" "$reset"
  printf '%s NRF DISCOVERY QUERY CHECK%s\n' "$blue" "$reset"
  printf '%s=========================================================%s\n' "$blue" "$reset"
  query_nrf 'AMF discovery' AMF "$amf_query_url"
  query_nrf 'SMF discovery' SMF "$smf_query_url"
  printf '\nNRF query check completed: %d check(s), %d failure(s).\n' "$checks" "$failures"
  ((failures == 0))
}

case "$action" in
  check)
    preflight || exit $?
    # This read-only audit must execute on every run.
    exit 10
    ;;
  apply)
    # Read-only feature; no system state is changed.
    :
    ;;
  verify)
    preflight && run_queries
    ;;
  rollback)
    # Read-only feature; no rollback is required.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback} [key=value ...]\n' "$0" >&2
    exit 2
    ;;
esac
