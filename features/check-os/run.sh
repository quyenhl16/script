#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"
required_commands="${SYSSETUP_PARAM_REQUIRED_COMMANDS:-bash python3 kubectl grep sed}"
minimum_bash_major="${SYSSETUP_PARAM_MINIMUM_BASH_MAJOR:-4}"
minimum_python_minor="${SYSSETUP_PARAM_MINIMUM_PYTHON_MINOR:-6}"
require_tcp_client="${SYSSETUP_PARAM_REQUIRE_TCP_CLIENT:-true}"

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
commands=()

pass() {
  checks=$((checks + 1))
  printf '  %s[PASS]%s %s\n' "$green" "$reset" "$1"
}

fail_check() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  printf '  %s[FAIL]%s %s\n' "$red" "$reset" "$1" >&2
}

skip() {
  printf '  %s[SKIP]%s %s\n' "$yellow" "$reset" "$1"
}

parse_arguments() {
  local argument
  for argument in "$@"; do
    case "$argument" in
      required_commands=*) required_commands="${argument#*=}" ;;
      minimum_bash_major=*) minimum_bash_major="${argument#*=}" ;;
      minimum_python_minor=*) minimum_python_minor="${argument#*=}" ;;
      require_tcp_client=*) require_tcp_client="${argument#*=}" ;;
      *)
        printf 'Unknown argument: %q\n' "$argument" >&2
        return 2
        ;;
    esac
  done
}

validate_configuration() {
  local command_name
  local normalized="${required_commands//,/ }"
  declare -A seen=()

  if [[ ! "$minimum_bash_major" =~ ^[1-9][0-9]*$ ]]; then
    printf 'minimum_bash_major must be a positive integer\n' >&2
    return 2
  fi
  if [[ ! "$minimum_python_minor" =~ ^[0-9]+$ ]]; then
    printf 'minimum_python_minor must be a non-negative integer\n' >&2
    return 2
  fi
  if [[ "$require_tcp_client" != true && "$require_tcp_client" != false ]]; then
    printf 'require_tcp_client must be true or false\n' >&2
    return 2
  fi

  read -r -a commands <<< "$normalized"
  if ((${#commands[@]} == 0)); then
    printf 'required_commands must not be empty\n' >&2
    return 2
  fi
  for command_name in "${commands[@]}"; do
    if [[ ! "$command_name" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]]; then
      printf 'Invalid command name: %q\n' "$command_name" >&2
      return 2
    fi
    if [[ -n "${seen[$command_name]:-}" ]]; then
      printf 'Duplicate command name: %q\n' "$command_name" >&2
      return 2
    fi
    seen[$command_name]=1
  done
}

check_operating_system() {
  local os_id
  local os_like
  local os_name

  if [[ ! -r /etc/os-release ]]; then
    fail_check '/etc/os-release is missing or unreadable'
    return
  fi
  # shellcheck disable=SC1091
  source /etc/os-release
  os_id="${ID:-unknown}"
  os_like="${ID_LIKE:-}"
  os_name="${PRETTY_NAME:-$os_id}"
  case "${os_id,,}:${os_like,,}" in
    *rhel*|*centos*|*rocky*|*almalinux*|*fedora*) pass "Operating system: $os_name" ;;
    *) fail_check "Unsupported operating system: $os_name" ;;
  esac
}

check_commands() {
  local command_name
  local command_path
  for command_name in "${commands[@]}"; do
    if command_path="$(command -v "$command_name" 2>/dev/null)"; then
      pass "Command [$command_name]: $command_path"
    else
      fail_check "Command [$command_name] was not found in PATH"
    fi
  done
}

check_versions() {
  local bash_major="${BASH_VERSINFO[0]:-0}"
  local bash_version="${BASH_VERSION:-unknown}"
  local python_version

  if ((bash_major >= 10#$minimum_bash_major)); then
    pass "Bash version $bash_version (minimum ${minimum_bash_major}.x)"
  else
    fail_check "Bash version $bash_version is older than ${minimum_bash_major}.x"
  fi

  if command -v python3 >/dev/null 2>&1; then
    python_version="$(python3 -c 'import platform; print(platform.python_version())' 2>/dev/null || true)"
    if python3 -c "import sys; raise SystemExit(0 if sys.version_info >= (3, $minimum_python_minor) else 1)"; then
      pass "Python version ${python_version:-unknown} (minimum 3.$minimum_python_minor)"
    else
      fail_check "Python version ${python_version:-unknown} is older than 3.$minimum_python_minor"
    fi
  else
    skip 'Python version check because python3 is missing'
  fi

  if command -v kubectl >/dev/null 2>&1; then
    if kubectl version --client >/dev/null 2>&1; then
      pass 'kubectl client can execute'
    else
      fail_check 'kubectl exists but its client version check failed'
    fi
  else
    skip 'kubectl client check because kubectl is missing'
  fi
}

check_tcp_dependency() {
  if [[ "$require_tcp_client" != true ]]; then
    skip 'TCP probe client is not required by configuration'
    return
  fi
  if command -v curl >/dev/null 2>&1 && \
     curl --version 2>/dev/null | grep -Eqi 'Protocols:.*[[:space:]]telnet([[:space:]]|$)'; then
    pass 'TCP probe client: curl with telnet protocol support'
  elif command -v telnet >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
    pass 'TCP probe client: telnet with timeout'
  else
    fail_check 'TCP probing requires curl with telnet support, or both telnet and timeout'
  fi
}

run_dependency_check() {
  validate_configuration || return
  printf '%sSystem dependency check%s\n' "$blue" "$reset"
  check_operating_system
  check_commands
  check_versions
  check_tcp_dependency
  printf '\nDependency check completed: %d check(s), %d failure(s).\n' "$checks" "$failures"
  ((failures == 0))
}

if (($# > 1)); then
  parse_arguments "${@:2}" || exit $?
fi

case "$action" in
  check|verify)
    run_dependency_check
    ;;
  apply|rollback)
    # Read-only audit; package installation remains an operator decision.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback} [key=value ...]\n' "$0" >&2
    exit 2
    ;;
esac
