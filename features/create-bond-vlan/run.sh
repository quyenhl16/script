#!/usr/bin/env bash
set -Eeuo pipefail

readonly CFG_DIR="/etc/sysconfig/network-scripts"

usage() {
  cat >&2 <<'EOF'
Usage:
  create_bond_vlan.sh <interface.vlan> [ip=<ipaddr>] [prefix=<prefix>] [gateway=<gateway>]

Example:
  create_bond_vlan.sh bond2.306 ip=10.0.36.87 prefix=24 gateway=10.0.36.254
EOF
  exit 2
}

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

(( EUID == 0 )) || fail "this script must run as root"
(( $# >= 1 )) || usage

ifname="$1"
shift
[[ "$ifname" =~ ^[a-zA-Z0-9_-]+\.([0-9]{1,4})$ ]] || \
  fail "interface must use <parent>.<vlan>, for example bond2.306"

vlan_id="${BASH_REMATCH[1]}"
(( 10#$vlan_id >= 1 && 10#$vlan_id <= 4094 )) || \
  fail "VLAN ID must be between 1 and 4094"

ipaddr=""
prefix=""
gateway=""
for argument in "$@"; do
  case "$argument" in
    ip=*) ipaddr="${argument#ip=}" ;;
    prefix=*) prefix="${argument#prefix=}" ;;
    gateway=*) gateway="${argument#gateway=}" ;;
    *) fail "unsupported argument: $argument" ;;
  esac
done

[[ -z "$ipaddr" ]] || valid_ipv4 "$ipaddr" || fail "invalid IPv4 address: $ipaddr"
[[ -z "$gateway" ]] || valid_ipv4 "$gateway" || fail "invalid gateway: $gateway"
if [[ -n "$prefix" ]]; then
  [[ "$prefix" =~ ^[0-9]{1,2}$ ]] || fail "prefix must be an integer between 0 and 32"
  (( 10#$prefix <= 32 )) || fail "prefix must be between 0 and 32"
fi
if [[ -n "$ipaddr" && -z "$prefix" ]] || [[ -z "$ipaddr" && -n "$prefix" ]]; then
  fail "ip and prefix must be supplied together"
fi
[[ -z "$gateway" || -n "$ipaddr" ]] || fail "gateway requires ip and prefix"
[[ -d "$CFG_DIR" ]] || fail "$CFG_DIR does not exist; this host may not use network-scripts"

cfg_file="${CFG_DIR}/ifcfg-${ifname}"
tmp_file="$(mktemp "${CFG_DIR}/.ifcfg-${ifname}.XXXXXX")"
cleanup() {
  rm -f -- "$tmp_file"
}
trap cleanup EXIT

{
  printf 'DEVICE=%s\n' "$ifname"
  printf 'BOOTPROTO=none\n'
  printf 'ONBOOT=yes\n'
  printf 'VLAN=yes\n'
  [[ -z "$ipaddr" ]] || printf 'IPADDR=%s\n' "$ipaddr"
  [[ -z "$prefix" ]] || printf 'PREFIX=%s\n' "$prefix"
  [[ -z "$gateway" ]] || printf 'GATEWAY=%s\n' "$gateway"
} > "$tmp_file"

if [[ -f "$cfg_file" ]] && cmp -s -- "$tmp_file" "$cfg_file"; then
  printf 'Configuration is already current: %s\n' "$cfg_file"
else
  if [[ -e "$cfg_file" ]]; then
    backup_file="${cfg_file}.bak.$(date +%Y%m%d%H%M%S)"
    cp -a -- "$cfg_file" "$backup_file"
    printf 'Backed up existing configuration: %s\n' "$backup_file"
  fi
  install -m 0644 -- "$tmp_file" "$cfg_file"
  printf 'Created configuration: %s\n' "$cfg_file"
fi

printf '%s\n' '-----'
cat -- "$cfg_file"
printf '%s\n' '-----'

if command -v nmcli >/dev/null 2>&1; then
  nmcli connection reload
  nmcli connection up "$ifname"
elif command -v ifup >/dev/null 2>&1; then
  ifup "$ifname"
else
  fail "configuration was written, but neither nmcli nor ifup is available"
fi

printf 'Done: %s is configured and active.\n' "$ifname"
