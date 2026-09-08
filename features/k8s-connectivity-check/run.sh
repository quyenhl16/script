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

curl_ausf_urls="${SYSSETUP_PARAM_CURL_AUSF_URLS:-http://68.240.36.153/}"
curl_udm_urls="${SYSSETUP_PARAM_CURL_UDM_URLS:-http://68.240.36.153/}"
curl_smf_urls="${SYSSETUP_PARAM_CURL_SMF_URLS:-http://68.240.36.105/}"
curl_amf_remote_urls="${SYSSETUP_PARAM_CURL_AMF_REMOTE_URLS:-http://68.240.36.9/}"
curl_nrf_urls="${SYSSETUP_PARAM_CURL_NRF_URLS:-http://68.240.36.193/}"
curl_connect_timeout="${SYSSETUP_PARAM_CURL_CONNECT_TIMEOUT_SECONDS:-3}"
curl_max_time="${SYSSETUP_PARAM_CURL_MAX_TIME_SECONDS:-10}"
curl_insecure="${SYSSETUP_PARAM_CURL_INSECURE:-false}"

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

validate_ipv4_list() {
  local name="$1"
  local value="$2"
  local address
  local normalized="${value//,/ }"
  local -a addresses

  read -r -a addresses <<< "$normalized"
  if ((${#addresses[@]} == 0)); then
    printf 'IPv4 list for %s must not be empty\n' "$name" >&2
    return 1
  fi
  for address in "${addresses[@]}"; do
    if ! validate_ipv4 "$address"; then
      printf 'Invalid IPv4 value in %s: %q\n' "$name" "$address" >&2
      return 1
    fi
  done
}

validate_url_list() {
  local name="$1"
  local value="$2"
  local url
  local authority
  local normalized="${value//,/ }"
  local -a urls

  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    printf 'URL list for %s must not contain line breaks\n' "$name" >&2
    return 1
  fi
  read -r -a urls <<< "$normalized"
  if ((${#urls[@]} == 0)); then
    printf 'URL list for %s must not be empty\n' "$name" >&2
    return 1
  fi
  for url in "${urls[@]}"; do
    if [[ ! "$url" =~ ^https?://[^/[:space:],]+(/[^[:space:],]*)?$ ]]; then
      printf 'Invalid HTTP(S) URL in %s: %q\n' "$name" "$url" >&2
      return 1
    fi
    authority="${url#*://}"
    authority="${authority%%/*}"
    if [[ "$authority" == *@* ]]; then
      printf 'URL userinfo is not allowed in %s: %q\n' "$name" "$url" >&2
      return 1
    fi
  done
}

validate_configuration() {
  local name
  local value
  local setting
  local -a single_ip_settings=(
    "vip_comm=$vip_comm"
    "vip_ipgw=$vip_ipgw"
    "vip_gtp=$vip_gtp"
  )
  local -a ip_list_settings=(
    "ip_ausf=$ip_ausf"
    "ip_udm=$ip_udm"
    "ip_smf=$ip_smf"
    "ip_amf_remote=$ip_amf_remote"
    "ip_nrf=$ip_nrf"
    "ip_gnodeb=$ip_gnodeb"
  )
  local -a url_list_settings=(
    "curl_ausf_urls=$curl_ausf_urls"
    "curl_udm_urls=$curl_udm_urls"
    "curl_smf_urls=$curl_smf_urls"
    "curl_amf_remote_urls=$curl_amf_remote_urls"
    "curl_nrf_urls=$curl_nrf_urls"
  )

  case "$phase" in
    vip|ospf|ping|curl|all) ;;
    *)
      printf 'Invalid phase %q; expected vip, ospf, ping, curl or all\n' "$phase" >&2
      return 2
      ;;
  esac

  if [[ ${#namespace} -gt 63 || ! "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
    printf 'Invalid Kubernetes namespace %q\n' "$namespace" >&2
    return 2
  fi

  for setting in "${single_ip_settings[@]}"; do
    name="${setting%%=*}"
    value="${setting#*=}"
    if ! validate_ipv4 "$value"; then
      printf 'Invalid IPv4 value for %s: %q\n' "$name" "$value" >&2
      return 2
    fi
  done

  for setting in "${ip_list_settings[@]}"; do
    name="${setting%%=*}"
    value="${setting#*=}"
    validate_ipv4_list "$name" "$value" || return 2
  done

  for setting in "${url_list_settings[@]}"; do
    name="${setting%%=*}"
    value="${setting#*=}"
    validate_url_list "$name" "$value" || return 2
  done

  if [[ ! "$curl_connect_timeout" =~ ^[1-9][0-9]*$ ]]; then
    printf 'curl_connect_timeout_seconds must be a positive integer\n' >&2
    return 2
  fi
  if [[ ! "$curl_max_time" =~ ^[1-9][0-9]*$ ]]; then
    printf 'curl_max_time_seconds must be a positive integer\n' >&2
    return 2
  fi
  if [[ "$curl_insecure" != true && "$curl_insecure" != false ]]; then
    printf 'curl_insecure must be true or false\n' >&2
    return 2
  fi
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
  local -a gnodeb_targets
  local -a ipgw_pods
  local -a mm_pods
  local -a nf_names=(AUSF UDM SMF AMF_REMOTE NRF)
  local -a target_ips
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
  read -r -a gnodeb_targets <<< "${ip_gnodeb//,/ }"
  if ((${#ipgw_pods[@]} == 0)); then
    fail 'No running IPGW pod found'
  else
    for pod in "${ipgw_pods[@]}"; do
      for target_ip in "${gnodeb_targets[@]}"; do
        if kubectl exec -n "$namespace" "$pod" -- \
          sudo -n ping -c 2 -W 2 -I "$vip_ipgw" "$target_ip" >/dev/null 2>&1; then
          pass "Pod [$pod]: $vip_ipgw -> gNodeB $target_ip"
        else
          fail "Pod [$pod]: $vip_ipgw cannot reach gNodeB $target_ip"
        fi
      done
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
        target_ips=()
        read -r -a target_ips <<< "${nf_targets[$nf_name]//,/ }"
        for target_ip in "${target_ips[@]}"; do
          if ping_from_pod "$pod" "$target_ip"; then
            pass "Pod [$pod]: path to $nf_name ($target_ip) is reachable"
          else
            fail "Pod [$pod]: path to $nf_name ($target_ip) is unreachable"
          fi
        done
      done
    done
  fi
}

url_for_log() {
  local value="$1"
  value="${value%%\?*}"
  value="${value%%#*}"
  printf '%s' "$value"
}

curl_from_pod() {
  local pod="$1"
  local url="$2"
  local response
  local http_code
  local status
  local detail
  local display_url
  local -a curl_args=(
    curl
    --silent
    --show-error
    --output /dev/null
    --write-out $'\n%{http_code}'
    --connect-timeout "$curl_connect_timeout"
    --max-time "$curl_max_time"
  )

  if [[ "$curl_insecure" == true ]]; then
    curl_args+=(--insecure)
  fi
  response="$(kubectl exec -n "$namespace" "$pod" -- "${curl_args[@]}" "$url" 2>&1)"
  status=$?
  http_code="${response##*$'\n'}"
  display_url="$(url_for_log "$url")"

  if ((status == 0)) && [[ "$http_code" =~ ^[1-5][0-9][0-9]$ ]]; then
    pass "Pod [$pod]: $display_url is reachable (HTTP $http_code)"
    return
  fi

  detail="${response//$'\r'/ }"
  detail="${detail//$'\n'/ }"
  detail="${detail:0:240}"
  [[ -n "$detail" ]] || detail="curl exited with status $status"
  fail "Pod [$pod]: $display_url is unreachable ($detail)"
}

phase_check_http() {
  local pod
  local nf_name
  local url
  local -a mm_pods
  local -a target_urls
  local -a nf_names=(AUSF UDM SMF AMF_REMOTE NRF)
  declare -A nf_urls=(
    [AUSF]="$curl_ausf_urls"
    [UDM]="$curl_udm_urls"
    [SMF]="$curl_smf_urls"
    [AMF_REMOTE]="$curl_amf_remote_urls"
    [NRF]="$curl_nrf_urls"
  )

  heading '[PHASE 4] VERIFYING PEER NF HTTP CONNECTIVITY (CURL)'
  mapfile -t mm_pods < <(get_running_pods mm)
  if ((${#mm_pods[@]} == 0)); then
    fail 'No running MM pods found for peer NF curl checks'
    return
  fi

  for pod in "${mm_pods[@]}"; do
    printf '\n  Pod: [%s]\n' "$pod"
    if ! kubectl exec -n "$namespace" "$pod" -- curl --version >/dev/null 2>&1; then
      fail "Pod [$pod]: curl is not available in the selected container"
      continue
    fi
    for nf_name in "${nf_names[@]}"; do
      target_urls=()
      read -r -a target_urls <<< "${nf_urls[$nf_name]//,/ }"
      for url in "${target_urls[@]}"; do
        printf '  NF: %-10s ' "$nf_name"
        curl_from_pod "$pod" "$url"
      done
    done
  done
}

run_checklist() {
  case "$phase" in
    vip) phase_check_vip ;;
    ospf) phase_check_ospf ;;
    ping) phase_check_connectivity ;;
    curl) phase_check_http ;;
    all)
      phase_check_vip
      printf '\n'
      phase_check_ospf
      printf '\n'
      phase_check_connectivity
      printf '\n'
      phase_check_http
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
