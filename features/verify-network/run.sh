#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

valid_ipv4() {
  local value="$1" index
  [[ "$value" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] || return 1
  for index in 1 2 3 4; do
    (( 10#${BASH_REMATCH[$index]} <= 255 )) || return 1
  done
}

(( $# > 0 )) || fail "enter at least one gateway IPv4 address"
command -v ping >/dev/null 2>&1 || fail "ping command was not found"

failures=0
for gateway in "$@"; do
  if ! valid_ipv4 "$gateway"; then
    printf '[FAIL] invalid gateway IPv4 address: %s\n' "$gateway" >&2
    failures=$((failures + 1))
    continue
  fi
  if ping -c 3 -W 2 "$gateway" >/dev/null 2>&1; then
    printf '[PASS] gateway %s is reachable\n' "$gateway"
  else
    printf '[FAIL] gateway %s is unreachable\n' "$gateway" >&2
    failures=$((failures + 1))
  fi
done

(( failures == 0 )) || fail "$failures gateway check(s) failed"
printf 'Done: all %d gateway(s) are reachable.\n' "$#"
