#!/usr/bin/env bash
set -Eeuo pipefail

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../../scripts/lib/common.sh
source "$FEATURE_DIR/../../scripts/lib/common.sh"

action="${1:-}"
package_text="${SYSSETUP_PARAM_PACKAGES:-curl wget git vim jq tar unzip}"
read -r -a packages <<< "$package_text"

check_packages() {
  local package_name
  for package_name in "${packages[@]}"; do
    rpm -q "$package_name" >/dev/null 2>&1 || return 10
  done
}

case "$action" in
  check)
    check_packages
    ;;
  apply)
    install_packages "${packages[@]}"
    ;;
  verify)
    check_packages
    ;;
  rollback)
    log_info 'Rollback is intentionally not supported for shared base packages'
    ;;
  *)
    echo "Usage: $0 {check|apply|verify|rollback}" >&2
    exit 2
    ;;
esac
