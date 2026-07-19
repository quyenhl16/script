#!/usr/bin/env bash
set -Eeuo pipefail

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../../scripts/lib/common.sh
source "$FEATURE_DIR/../../scripts/lib/common.sh"

action="${1:-}"

is_ready() {
  rpm -q openssh-server >/dev/null 2>&1 &&
    systemctl is-enabled --quiet sshd &&
    systemctl is-active --quiet sshd
}

case "$action" in
  check)
    is_ready || exit 10
    ;;
  apply)
    install_packages openssh-server
    enable_service sshd
    ;;
  verify)
    is_ready
    ;;
  rollback)
    systemctl disable --now sshd
    ;;
  *)
    echo "Usage: $0 {check|apply|verify|rollback}" >&2
    exit 2
    ;;
esac
