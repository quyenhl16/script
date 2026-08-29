#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  create-local-path <absolute-path> [absolute-path ...] [mode=0755] [owner=root] [group=root]

Example:
  create-local-path /data/app /data/log /opt/company/cache mode=0755 owner=root group=root

Paths containing whitespace are not supported by the dashboard argument field.
EOF
  exit 2
}

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

(( EUID == 0 )) || fail "this script must run as root"
(( $# >= 1 )) || usage

mode="0755"
owner="root"
group="root"
declare -a paths=()

for argument in "$@"; do
  case "$argument" in
    mode=*) mode="${argument#mode=}" ;;
    owner=*) owner="${argument#owner=}" ;;
    group=*) group="${argument#group=}" ;;
    /*) paths+=("$argument") ;;
    *) fail "unsupported argument or non-absolute path: $argument" ;;
  esac
done

(( ${#paths[@]} > 0 )) || fail "enter at least one absolute directory path"
[[ "$mode" =~ ^0?[0-7]{3,4}$ ]] || fail "mode must be an octal value such as 0755 or 1770"
[[ "$owner" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]*\$?$ ]] || fail "invalid owner: $owner"
[[ "$group" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]*\$?$ ]] || fail "invalid group: $group"
getent passwd "$owner" >/dev/null || fail "owner does not exist: $owner"
getent group "$group" >/dev/null || fail "group does not exist: $group"

for path in "${paths[@]}"; do
  [[ "$path" != "/" ]] || fail "refusing to manage the filesystem root"
  [[ "$path" != *"//"* ]] || fail "path contains an empty component: $path"
  case "/${path#/}/" in
    */./*|*/../*) fail "path contains '.' or '..' component: $path" ;;
  esac
  if [[ -e "$path" && ! -d "$path" ]]; then
    fail "path exists but is not a directory: $path"
  fi

  status="CREATED"
  [[ ! -d "$path" ]] || status="UPDATED"
  install -d -m "$mode" -o "$owner" -g "$group" -- "$path"
  printf '[%s] %s owner=%s group=%s mode=%s\n' "$status" "$path" "$owner" "$group" "$mode"
done

printf 'Done: %d path(s) are ready.\n' "${#paths[@]}"
