#!/usr/bin/env bash
set -uo pipefail

action="${1:-}"
phase="${SYSSETUP_PARAM_PHASE:-all}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-pramf01}"

vip_comm="${SYSSETUP_PARAM_VIP_COMM:-68.240.36.9}"
vip_ipgw="${SYSSETUP_PARAM_VIP_IPGW:-68.240.36.1}"
vip_gtp="${SYSSETUP_PARAM_VIP_GTP:-68.240.36.96}"

ip_ausf="${SYSSETUP_PARAM_IP_AUSF:-68.240.36.153}"
ip_udm="${SYSSETUP_PARAM_IP_UDM:-68.240.36.153}"
ip_smf="${SYSSETUP_PARAM_IP_SMF:-68.240.36.105}"
ip_amf_remote="${SYSSETUP_PARAM_IP_AMF_REMOTE:-68.240.36.9}"
ip_nrf="${SYSSETUP_PARAM_IP_NRF:-68.240.36.193}"
ip_gnodeb="${SYSSETUP_PARAM_IP_GNODEB:-69.69.0.150}"

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

failures=0
checks=0

heading() {
  printf '%s=========================================================%s\n' "$blue" "$reset"
  printf '%s %s%s\n' "$blue" "$1" "$reset"
  printf '%s=========================================================%s\n' "$blue" "$reset"
}

pass() {
  checks=$((checks + 1))
  printf '    %s[PASS]%s %s\n' "$green" "$reset" "$1"
}

fail() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  printf '    %s[FAIL]%s %s\n' "$red" "$reset" "$1" >&2
}

validate_ipv4() {
  local address="$1"
  local octet
  local -a octets

  [[ "$address" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  IFS='.' read -r -a octets <<< "$address"
  [[ ${#octets[@]} -eq 4 ]] || return 1
  for octet in "${octets[@]}"; do
    ((10#$octet <= 255)) || return 1
  done
}

validate_configuration() {
  local name
  local value

  case "$phase" in
    vip|ospf|ping|all) ;;
    *)
      printf 'Invalid phase %q; expected vip, ospf, ping or all\n' "$phase" >&2
      return 2
      ;;
  esac

  if [[ ${#namespace} -gt 63 || ! "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
    printf 'Invalid Kubernetes namespace %q\n' "$namespace" >&2
    return 2
  fi

  while IFS='=' read -r name value; do
    if ! validate_ipv4 "$value"; then
      printf 'Invalid IPv4 value for %s: %q\n' "$name" "$value" >&2
      return 2
    fi
  done <<EOF
vip_comm=$vip_comm
vip_ipgw=$vip_ipgw
vip_gtp=$vip_gtp
ip_ausf=$ip_ausf
ip_udm=$ip_udm
ip_smf=$ip_smf
ip_amf_remote=$ip_amf_remote
ip_nrf=$ip_nrf
ip_gnodeb=$ip_gnodeb
EOF
}

preflight() {
  validate_configuration || return
  if ! command -v kubectl >/dev/null 2>&1; then
    printf 'kubectl is required but was not found in PATH\n' >&2
    return 1
  fi
  if ! kubectl get namespace "$namespace" >/dev/null 2>&1; then
    printf 'Cannot access Kubernetes namespace %q\n' "$namespace" >&2
    return 1
  fi
}

get_running_pods() {
  local prefix="$1"
  kubectl get pods -n "$namespace" \
    --field-selector=status.phase=Running \
    --no-headers \
    -o 'custom-columns=:metadata.name' 2>/dev/null | grep -E "^${prefix}-" || true
}

phase_check_vip() {
  local prefix
  local target_vip
  local pod
  local -a pods
  local -a prefixes=(comm ipgw gtp)
  declare -A pod_vip_map=(
    [comm]="$vip_comm"
    [ipgw]="$vip_ipgw"
    [gtp]="$vip_gtp"
  )

  heading '[PHASE 1] VERIFYING IP VIP ON LOOPBACK (LO) INTERFACE'
  for prefix in "${prefixes[@]}"; do
    target_vip="${pod_vip_map[$prefix]}"
    printf '\n  Scanning pod group: %s%s%s (expected VIP: %s)\n' \
      "$yellow" "$prefix" "$reset" "$target_vip"
    mapfile -t pods < <(get_running_pods "$prefix")
    if ((${#pods[@]} == 0)); then
      fail "No running pods found for group $prefix"
      continue
    fi

    for pod in "${pods[@]}"; do
      if kubectl exec -n "$namespace" "$pod" -- ip address show lo 2>/dev/null | grep -Fq -- "$target_vip"; then
        pass "Pod [$pod]: VIP $target_vip found"
      else
        fail "Pod [$pod]: VIP $target_vip is missing from loopback"
      fi
    done
  done
}

phase_check_ospf() {
  local prefix
  local pod
  local ospf_out
  local -a pods
  local -a prefixes=(comm gtp ipgw)

  heading '[PHASE 2] VERIFYING OSPF NEIGHBOR STATES (VTYSH)'
  for prefix in "${prefixes[@]}"; do
    printf '\n  Scanning pod group: %s%s%s\n' "$yellow" "$prefix" "$reset"
    mapfile -t pods < <(get_running_pods "$prefix")
    if ((${#pods[@]} == 0)); then
      fail "No running pods found for group $prefix"
      continue
    fi

    for pod in "${pods[@]}"; do
      if ! ospf_out="$(kubectl exec -n "$namespace" "$pod" -- \
        sudo -n vtysh -c 'show ip ospf neighbor' 2>/dev/null)"; then
        fail "Pod [$pod]: could not execute vtysh with sudo"
      elif grep -q 'Full' <<< "$ospf_out"; then
        pass "Pod [$pod]: active FULL adjacency detected"
        sed 's/^/      /' <<< "$ospf_out"
      else
        fail "Pod [$pod]: no OSPF neighbor is in FULL state"
        [[ -z "$ospf_out" ]] || sed 's/^/      /' <<< "$ospf_out"
      fi
    done
  done
}

ping_from_pod() {
  local pod="$1"
  local target="$2"
  kubectl exec -n "$namespace" "$pod" -- ping -c 2 -W 2 "$target" >/dev/null 2>&1
}

phase_check_connectivity() {
  local pod
  local nf_name
  local target_ip
  local prober_mm_pod=''
  local -a ipgw_pods
  local -a mm_pods
  local -a nf_names=(AUSF UDM SMF AMF_REMOTE NRF)
  declare -A nf_targets=(
    [AUSF]="$ip_ausf"
    [UDM]="$ip_udm"
    [SMF]="$ip_smf"
    [AMF_REMOTE]="$ip_amf_remote"
    [NRF]="$ip_nrf"
  )

  heading '[PHASE 3] VERIFYING NETWORK CONNECTIVITY (PING TESTS)'
  mapfile -t mm_pods < <(get_running_pods mm)
  if ((${#mm_pods[@]} > 0)); then
    prober_mm_pod="${mm_pods[0]}"
  fi

  printf '\n%s 3.1 N2: IPGW VIP -> gNodeB%s\n' "$yellow" "$reset"
  mapfile -t ipgw_pods < <(get_running_pods ipgw)
  if ((${#ipgw_pods[@]} == 0)); then
    fail 'No running IPGW pod found'
  else
    for pod in "${ipgw_pods[@]}"; do
      if kubectl exec -n "$namespace" "$pod" -- \
        sudo -n ping -c 2 -W 2 -I "$vip_ipgw" "$ip_gnodeb" >/dev/null 2>&1; then
        pass "Pod [$pod]: $vip_ipgw -> gNodeB $ip_gnodeb"
      else
        fail "Pod [$pod]: $vip_ipgw cannot reach gNodeB $ip_gnodeb"
      fi
    done
  fi

  printf '\n%s 3.2 SBI-Server: MM pod -> COMM VIP%s\n' "$yellow" "$reset"
  if [[ -z "$prober_mm_pod" ]]; then
    fail 'No running MM pod found to act as prober'
  elif ping_from_pod "$prober_mm_pod" "$vip_comm"; then
    pass "Pod [$prober_mm_pod]: COMM VIP $vip_comm is reachable"
  else
    fail "Pod [$prober_mm_pod]: COMM VIP $vip_comm is unreachable"
  fi

  printf '\n%s 3.3 N26: MM pod -> GTP VIP%s\n' "$yellow" "$reset"
  if [[ -z "$prober_mm_pod" ]]; then
    fail 'No running MM pod found to act as prober'
  elif ping_from_pod "$prober_mm_pod" "$vip_gtp"; then
    pass "Pod [$prober_mm_pod]: GTP VIP $vip_gtp is reachable"
  else
    fail "Pod [$prober_mm_pod]: GTP VIP $vip_gtp is unreachable"
  fi

  printf '\n%s 3.4 SBI-Client: MM pods -> peer NFs%s\n' "$yellow" "$reset"
  if ((${#mm_pods[@]} == 0)); then
    fail 'No running MM pods found for peer NF checks'
  else
    for pod in "${mm_pods[@]}"; do
      printf '  Pod: [%s]\n' "$pod"
      for nf_name in "${nf_names[@]}"; do
        target_ip="${nf_targets[$nf_name]}"
        if ping_from_pod "$pod" "$target_ip"; then
          pass "Path to $nf_name ($target_ip) is reachable"
        else
          fail "Path to $nf_name ($target_ip) is unreachable"
        fi
      done
    done
  fi
}

run_checklist() {
  case "$phase" in
    vip) phase_check_vip ;;
    ospf) phase_check_ospf ;;
    ping) phase_check_connectivity ;;
    all)
      phase_check_vip
      printf '\n'
      phase_check_ospf
      printf '\n'
      phase_check_connectivity
      ;;
  esac

  printf '\nChecklist completed: %d check(s), %d failure(s).\n' "$checks" "$failures"
  ((failures == 0))
}

case "$action" in
  check)
    preflight || exit $?
    # An audit must run on every invocation, so tell the runner to continue.
    exit 10
    ;;
  apply)
    # This feature is read-only; there is no system state to apply.
    :
    ;;
  verify)
    preflight && run_checklist
    ;;
  rollback)
    # This feature is read-only; there is no state to roll back.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
