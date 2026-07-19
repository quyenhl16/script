#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"

supported_os() {
  [[ -r /etc/os-release ]] || return 1
  # shellcheck disable=SC1091
  source /etc/os-release
  case "${ID,,}:${ID_LIKE,,}" in
    *rhel*|*centos*|*rocky*|*almalinux*|*fedora*) return 0 ;;
    *) return 1 ;;
  esac
}

case "$action" in
  check)
    supported_os || exit 1
    ;;
  apply)
    :
    ;;
  verify)
    supported_os
    ;;
  rollback)
    :
    ;;
  *)
    echo "Usage: $0 {check|apply|verify|rollback}" >&2
    exit 2
    ;;
esac
