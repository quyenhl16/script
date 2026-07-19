#!/usr/bin/env bash

log_info() {
  printf '[INFO] %s\n' "$*"
}

log_error() {
  printf '[ERROR] %s\n' "$*" >&2
}

package_manager() {
  if command -v dnf >/dev/null 2>&1; then
    printf 'dnf'
  elif command -v yum >/dev/null 2>&1; then
    printf 'yum'
  else
    log_error 'Neither dnf nor yum was found'
    return 20
  fi
}

install_packages() {
  local manager
  manager="$(package_manager)"
  "$manager" install -y "$@"
}

enable_service() {
  local service_name="$1"
  systemctl enable --now "$service_name"
}
