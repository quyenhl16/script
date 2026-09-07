#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
input_file="${SYSSETUP_PARAM_INPUT_FILE:-}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-pramf01}"
sheet="${SYSSETUP_PARAM_SHEET:-VDU}"
header_row="${SYSSETUP_PARAM_HEADER_ROW:-1}"
attribute_column="${SYSSETUP_PARAM_ATTRIBUTE_COLUMN:-B}"
service_start_column="${SYSSETUP_PARAM_SERVICE_START_COLUMN:-D}"
mapping_file="${SYSSETUP_PARAM_MAPPING_FILE:-}"
cpu_default_unit="${SYSSETUP_PARAM_CPU_DEFAULT_UNIT:-m}"
memory_default_unit="${SYSSETUP_PARAM_MEMORY_DEFAULT_UNIT:-Mi}"
extra_policy="${SYSSETUP_PARAM_EXTRA_POLICY:-warn}"
ignore_extra="${SYSSETUP_PARAM_IGNORE_EXTRA:-}"
show_pass="${SYSSETUP_PARAM_SHOW_PASS:-false}"

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 2
}

validate_configuration() {
  [[ -n "$input_file" ]] || fail 'input_file parameter is required'
  [[ -f "$input_file" && -r "$input_file" ]] || fail "input_file is not a readable regular file: $input_file"
  [[ "${input_file,,}" == *.xlsx ]] || fail 'input_file must use the .xlsx extension'
  [[ -n "$sheet" ]] || fail 'sheet must not be empty'
  [[ "$header_row" =~ ^[0-9]+$ ]] && ((10#$header_row >= 1)) || fail 'header_row must be a positive integer'
  [[ "$attribute_column" =~ ^[A-Za-z]+$ ]] || fail 'attribute_column must be an Excel column name'
  [[ "$service_start_column" =~ ^[A-Za-z]+$ ]] || fail 'service_start_column must be an Excel column name'
  [[ ${#namespace} -le 63 && "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || fail "invalid Kubernetes namespace: $namespace"
  [[ -z "$mapping_file" || ( -f "$mapping_file" && -r "$mapping_file" ) ]] || fail "mapping_file is not readable: $mapping_file"
  [[ "$cpu_default_unit" =~ ^(n|u|m)?$ ]] || fail 'cpu_default_unit must be n, u, m or empty'
  [[ "$memory_default_unit" =~ ^(n|u|m|k|K|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?$ ]] || fail 'memory_default_unit is invalid'
  [[ "$show_pass" == true || "$show_pass" == false ]] || fail 'show_pass must be true or false'
  case "$extra_policy" in
    fail|warn|ignore) ;;
    *) fail 'extra_policy must be fail, warn or ignore' ;;
  esac
  [[ -f "$script_dir/check.py" && -r "$script_dir/check.py" ]] || fail "checker was not found: $script_dir/check.py"
}

preflight() {
  validate_configuration || return
  command -v python3 >/dev/null 2>&1 || { printf 'python3 is required but was not found in PATH\n' >&2; return 1; }
  command -v kubectl >/dev/null 2>&1 || { printf 'kubectl is required but was not found in PATH\n' >&2; return 1; }
  kubectl get namespace "$namespace" >/dev/null 2>&1 || { printf 'Cannot access Kubernetes namespace %q\n' "$namespace" >&2; return 1; }
}

run_check() {
  local -a command=(
    python3 "$script_dir/check.py"
    --input "$input_file"
    --namespace "$namespace"
    --sheet "$sheet"
    --header-row "$header_row"
    --attribute-column "${attribute_column^^}"
    --service-start-column "${service_start_column^^}"
    --cpu-default-unit "$cpu_default_unit"
    --memory-default-unit "$memory_default_unit"
    --extra-policy "$extra_policy"
    --ignore-extra "$ignore_extra"
    --show-pass "$show_pass"
  )
  if [[ -n "$mapping_file" ]]; then
    command+=(--mapping-file "$mapping_file")
  fi
  "${command[@]}"
}

case "$action" in
  check)
    preflight || exit $?
    # This audit must execute on every run.
    exit 10
    ;;
  apply)
    # Read-only feature; no system state is changed.
    :
    ;;
  verify)
    preflight && run_check
    ;;
  rollback)
    # Read-only feature; no rollback is required.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
