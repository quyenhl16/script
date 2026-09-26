#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 2
}

action="${1:-}"
source_xml="${SYSSETUP_PARAM_SOURCE_XML:-}"
target_xml="${SYSSETUP_PARAM_TARGET_XML:-}"
rules_file="${SYSSETUP_PARAM_RULES_FILE:-checks/xml/system-critical-paths.json}"
show_equal="${SYSSETUP_PARAM_SHOW_EQUAL:-false}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

parse_workflow_arguments() {
  local argument
  for argument in "$@"; do
    case "$argument" in
      source_xml=*) source_xml="${argument#*=}" ;;
      target_xml=*) target_xml="${argument#*=}" ;;
      rules_file=*) rules_file="${argument#*=}" ;;
      show_equal=*) show_equal="${argument#*=}" ;;
      *)
        printf 'Unknown argument: %q\n' "$argument" >&2
        return 2
        ;;
    esac
  done
}

if (($# > 1)); then
  parse_workflow_arguments "${@:2}" || exit $?
fi

preflight() {
  [[ -n "$source_xml" ]] || fail "source_xml parameter is required"
  [[ -f "$source_xml" && -r "$source_xml" ]] || fail "source XML is not a readable regular file: $source_xml"
  [[ -n "$target_xml" ]] || fail "target_xml parameter is required"
  [[ -f "$target_xml" && -r "$target_xml" ]] || fail "target XML is not a readable regular file: $target_xml"
  [[ -n "$rules_file" ]] || fail "rules_file parameter is required"
  [[ -f "$rules_file" && -r "$rules_file" ]] || fail "rules file is not a readable regular file: $rules_file"
  [[ "$show_equal" == "true" || "$show_equal" == "false" ]] || fail "show_equal must be true or false"
  command -v python3 >/dev/null 2>&1 || fail "python3 command was not found"
  [[ -f "$script_dir/compare.py" ]] || fail "XML comparison helper was not found: $script_dir/compare.py"
}

case "$action" in
  check)
    preflight
    # Comparison is read-only but must run for every invocation.
    exit 10
    ;;
  apply)
    :
    ;;
  verify)
    preflight
    python3 "$script_dir/compare.py" "$source_xml" "$target_xml" "$rules_file" "$show_equal"
    ;;
  rollback)
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback} [key=value ...]\n' "$0" >&2
    exit 2
    ;;
esac
