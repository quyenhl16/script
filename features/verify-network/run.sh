#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

(( $# > 0 )) || fail "enter at least one bond VLAN interface"
command -v ip >/dev/null 2>&1 || fail "ip command was not found"

failures=0
for interface in "$@"; do
  if [[ ! "$interface" =~ ^[a-zA-Z0-9_-]+\.([0-9]{1,4})$ ]] || \
     (( 10#${BASH_REMATCH[1]:-0} < 1 || 10#${BASH_REMATCH[1]:-0} > 4094 )); then
    printf '[FAIL] invalid bond VLAN interface: %s\n' "$interface" >&2
    failures=$((failures + 1))
    continue
  fi

  if ! link_info="$(ip -o link show dev "$interface" 2>/dev/null)"; then
    printf '[FAIL] bond VLAN %s does not exist\n' "$interface" >&2
    failures=$((failures + 1))
    continue
  fi

  state="UNKNOWN"
  if [[ "$link_info" =~ state[[:space:]]+([^[:space:]]+) ]]; then
    state="${BASH_REMATCH[1]}"
  fi
  if [[ "$state" == "UP" ]]; then
    printf '[PASS] bond VLAN %s exists and is UP\n' "$interface"
  else
    printf '[FAIL] bond VLAN %s exists but state is %s\n' "$interface" "$state" >&2
    failures=$((failures + 1))
  fi
done

(( failures == 0 )) || fail "$failures bond VLAN check(s) failed"
printf 'Done: all %d bond VLAN interface(s) exist and are UP.\n' "$#"
