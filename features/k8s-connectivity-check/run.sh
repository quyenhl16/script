#!/usr/bin/env bash
set -uo pipefail

action="${1:-}"
phase="${SYSSETUP_PARAM_PHASE:-all}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-pramf01}"

vip_comm="${SYSSETUP_PARAM_VIP_COMM:-68.240.36.9}"
vip_ipgw="${SYSSETUP_PARAM_VIP_IPGW:-68.240.36.1}"
vip_gtp="${SYSSETUP_PARAM_VIP_GTP:-68.240.36.96}"

ip_ausf="${SYSSETUP_PARAM_IP_AUSF-68.240.36.153}"
ip_udm="${SYSSETUP_PARAM_IP_UDM-68.240.36.153}"
ip_smf="${SYSSETUP_PARAM_IP_SMF-68.240.36.105}"
ip_amf_self="${SYSSETUP_PARAM_IP_AMF_SELF:-}"
ip_amf_remote="${SYSSETUP_PARAM_IP_AMF_REMOTE-68.240.36.9}"
ip_nrf="${SYSSETUP_PARAM_IP_NRF-68.240.36.193}"
ip_eir="${SYSSETUP_PARAM_IP_EIR-68.240.136.1}"
ip_nssf="${SYSSETUP_PARAM_IP_NSSF-68.240.136.2}"
ip_gnodeb="${SYSSETUP_PARAM_IP_GNODEB-69.69.0.150}"
ping_count="${SYSSETUP_PARAM_PING_COUNT:-2}"
ping_timeout="${SYSSETUP_PARAM_PING_TIMEOUT_SECONDS:-2}"
parallel_interfaces="${SYSSETUP_PARAM_PARALLEL_INTERFACES:-true}"
max_parallel_pods="${SYSSETUP_PARAM_MAX_PARALLEL_PODS:-10}"
max_parallel_nfs_per_pod="${SYSSETUP_PARAM_MAX_PARALLEL_NFS_PER_POD:-1}"
max_total_parallel_jobs="${SYSSETUP_PARAM_MAX_TOTAL_PARALLEL_JOBS:-10}"

curl_ausf_urls="${SYSSETUP_PARAM_CURL_AUSF_URLS-http://68.240.36.153/}"
curl_udm_urls="${SYSSETUP_PARAM_CURL_UDM_URLS-http://68.240.36.153/}"
curl_smf_urls="${SYSSETUP_PARAM_CURL_SMF_URLS-http://68.240.36.105/}"
curl_amf_self_urls="${SYSSETUP_PARAM_CURL_AMF_SELF_URLS:-}"
curl_amf_remote_urls="${SYSSETUP_PARAM_CURL_AMF_REMOTE_URLS-http://68.240.36.9/}"
curl_nrf_urls="${SYSSETUP_PARAM_CURL_NRF_URLS-http://68.240.36.193/}"
curl_eir_urls="${SYSSETUP_PARAM_CURL_EIR_URLS-http://68.240.136.1/}"
curl_nssf_urls="${SYSSETUP_PARAM_CURL_NSSF_URLS-http://68.240.136.2/}"
curl_connect_timeout="${SYSSETUP_PARAM_CURL_CONNECT_TIMEOUT_SECONDS:-3}"
curl_max_time="${SYSSETUP_PARAM_CURL_MAX_TIME_SECONDS:-10}"
curl_insecure="${SYSSETUP_PARAM_CURL_INSECURE:-false}"

parse_workflow_arguments() {
  local argument
  for argument in "$@"; do
    case "$argument" in
      phase=*) phase="${argument#*=}" ;;
      namespace=*) namespace="${argument#*=}" ;;
      vip_comm=*) vip_comm="${argument#*=}" ;;
      vip_ipgw=*) vip_ipgw="${argument#*=}" ;;
      vip_gtp=*) vip_gtp="${argument#*=}" ;;
      ip_ausf=*) ip_ausf="${argument#*=}" ;;
      ip_udm=*) ip_udm="${argument#*=}" ;;
      ip_smf=*) ip_smf="${argument#*=}" ;;
      ip_amf_self=*) ip_amf_self="${argument#*=}" ;;
      ip_amf_remote=*) ip_amf_remote="${argument#*=}" ;;
      ip_nrf=*) ip_nrf="${argument#*=}" ;;
      ip_eir=*) ip_eir="${argument#*=}" ;;
      ip_nssf=*) ip_nssf="${argument#*=}" ;;
      ip_gnodeb=*) ip_gnodeb="${argument#*=}" ;;
      ping_count=*) ping_count="${argument#*=}" ;;
      ping_timeout_seconds=*) ping_timeout="${argument#*=}" ;;
      parallel_interfaces=*) parallel_interfaces="${argument#*=}" ;;
      max_parallel_pods=*) max_parallel_pods="${argument#*=}" ;;
      max_parallel_nfs_per_pod=*) max_parallel_nfs_per_pod="${argument#*=}" ;;
      max_total_parallel_jobs=*) max_total_parallel_jobs="${argument#*=}" ;;
      curl_ausf_urls=*) curl_ausf_urls="${argument#*=}" ;;
      curl_udm_urls=*) curl_udm_urls="${argument#*=}" ;;
      curl_smf_urls=*) curl_smf_urls="${argument#*=}" ;;
      curl_amf_self_urls=*) curl_amf_self_urls="${argument#*=}" ;;
      curl_amf_remote_urls=*) curl_amf_remote_urls="${argument#*=}" ;;
      curl_nrf_urls=*) curl_nrf_urls="${argument#*=}" ;;
      curl_eir_urls=*) curl_eir_urls="${argument#*=}" ;;
      curl_nssf_urls=*) curl_nssf_urls="${argument#*=}" ;;
      curl_connect_timeout_seconds=*) curl_connect_timeout="${argument#*=}" ;;
      curl_max_time_seconds=*) curl_max_time="${argument#*=}" ;;
      curl_insecure=*) curl_insecure="${argument#*=}" ;;
      *)
        printf 'Unknown argument: %q\n' "$argument" >&2
        return 2
        ;;
    esac
  done
}

if (($# > 1)); then
  parse_workflow_arguments "${@:2}" || exit $?
fi

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
    "ip_amf_self=$ip_amf_self"
    "ip_amf_remote=$ip_amf_remote"
    "ip_nrf=$ip_nrf"
    "ip_eir=$ip_eir"
    "ip_nssf=$ip_nssf"
    "ip_gnodeb=$ip_gnodeb"
  )
  local -a url_list_settings=(
    "curl_ausf_urls=$curl_ausf_urls"
    "curl_udm_urls=$curl_udm_urls"
    "curl_smf_urls=$curl_smf_urls"
    "curl_amf_self_urls=$curl_amf_self_urls"
    "curl_amf_remote_urls=$curl_amf_remote_urls"
    "curl_nrf_urls=$curl_nrf_urls"
    "curl_eir_urls=$curl_eir_urls"
    "curl_nssf_urls=$curl_nssf_urls"
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
    [[ -z "$value" ]] && continue
    validate_ipv4_list "$name" "$value" || return 2
  done

  for setting in "${url_list_settings[@]}"; do
    name="${setting%%=*}"
    value="${setting#*=}"
    [[ -z "$value" ]] && continue
    validate_url_list "$name" "$value" || return 2
  done

  if [[ ! "$ping_count" =~ ^[1-9][0-9]*$ ]]; then
    printf 'ping_count must be a positive integer\n' >&2
    return 2
  fi
  if [[ ! "$ping_timeout" =~ ^[1-9][0-9]*$ ]]; then
    printf 'ping_timeout_seconds must be a positive integer\n' >&2
    return 2
  fi
  if [[ "$parallel_interfaces" != true && "$parallel_interfaces" != false ]]; then
    printf 'parallel_interfaces must be true or false\n' >&2
    return 2
  fi
  if [[ ! "$max_parallel_pods" =~ ^[1-9][0-9]*$ ]]; then
    printf 'max_parallel_pods must be a positive integer\n' >&2
    return 2
  fi
  if [[ ! "$max_parallel_nfs_per_pod" =~ ^[1-9][0-9]*$ ]]; then
    printf 'max_parallel_nfs_per_pod must be a positive integer\n' >&2
    return 2
  fi
  if [[ ! "$max_total_parallel_jobs" =~ ^[1-9][0-9]*$ ]]; then
    printf 'max_total_parallel_jobs must be a positive integer\n' >&2
    return 2
  fi

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

parallel_root=''
declare -a background_pids=()

parallel_runtime_cleanup() {
  local pid
  for pid in "${background_pids[@]}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  for pid in "${background_pids[@]}"; do
    wait "$pid" >/dev/null 2>&1 || true
  done
  background_pids=()
  if [[ -n "$parallel_root" && -d "$parallel_root" ]]; then
    rm -rf -- "$parallel_root"
  fi
}

parallel_runtime_init() {
  parallel_root="$(mktemp -d "${TMPDIR:-/tmp}/k8s-connectivity-check.XXXXXX")" || return 1
  mkdir -p "$parallel_root/pod-slots" "$parallel_root/job-slots"

  trap parallel_runtime_cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
}

acquire_parallel_slot() {
  local pool="$1"
  local limit="$2"
  local index
  local slot

  while true; do
    for ((index = 0; index < limit; index++)); do
      slot="$pool/$index"
      if mkdir "$slot" 2>/dev/null; then
        printf '%s' "$slot"
        return
      fi
    done
    sleep 0.02
  done
}

release_parallel_slot() {
  rmdir -- "$1" 2>/dev/null || true
}

launch_background() {
  "$@" &
  background_pids+=("$!")
}

wait_for_background() {
  local pid
  for pid in "${background_pids[@]}"; do
    wait "$pid" || true
  done
  background_pids=()
}

write_probe_result() {
  local path="$1"
  local status="$2"
  local message="$3"
  printf '%s\n%s\n' "$status" "$message" > "$path"
}

render_probe_results() {
  local directory="$1"
  local file
  local status
  local message

  for file in "$directory"/*.result; do
    [[ -f "$file" ]] || continue
    IFS= read -r status < "$file"
    message="$(tail -n +2 "$file")"
    if [[ "$status" == PASS ]]; then
      pass "$message"
    else
      fail "$message"
    fi
  done
}

ping_probe_task() {
  local pod="$1"
  local mode="$2"
  local label="$3"
  local target="$4"
  local result_file="$5"
  local job_slot

  job_slot="$(acquire_parallel_slot "$parallel_root/job-slots" "$max_total_parallel_jobs")"
  case "$mode" in
    gnodeb)
      if kubectl exec -n "$namespace" "$pod" -- \
        sudo -n ping -c "$ping_count" -W "$ping_timeout" -I "$vip_ipgw" "$target" >/dev/null 2>&1; then
        write_probe_result "$result_file" PASS "Pod [$pod]: $vip_ipgw -> gNodeB $target"
      else
        write_probe_result "$result_file" FAIL "Pod [$pod]: $vip_ipgw cannot reach gNodeB $target"
      fi
      ;;
    comm)
      if kubectl exec -n "$namespace" "$pod" -- \
        ping -c "$ping_count" -W "$ping_timeout" "$target" >/dev/null 2>&1; then
        write_probe_result "$result_file" PASS "Pod [$pod]: COMM VIP $target is reachable"
      else
        write_probe_result "$result_file" FAIL "Pod [$pod]: COMM VIP $target is unreachable"
      fi
      ;;
    gtp)
      if kubectl exec -n "$namespace" "$pod" -- \
        ping -c "$ping_count" -W "$ping_timeout" "$target" >/dev/null 2>&1; then
        write_probe_result "$result_file" PASS "Pod [$pod]: GTP VIP $target is reachable"
      else
        write_probe_result "$result_file" FAIL "Pod [$pod]: GTP VIP $target is unreachable"
      fi
      ;;
    peer)
      if kubectl exec -n "$namespace" "$pod" -- \
        ping -c "$ping_count" -W "$ping_timeout" "$target" >/dev/null 2>&1; then
        write_probe_result "$result_file" PASS "Pod [$pod]: path to $label ($target) is reachable"
      else
        write_probe_result "$result_file" FAIL "Pod [$pod]: path to $label ($target) is unreachable"
      fi
      ;;
  esac
  release_parallel_slot "$job_slot"
}

run_ping_pod_worker() {
  local pod="$1"
  local manifest="$2"
  local mode
  local label
  local target
  local result_file
  local pid
  local pod_slot
  local -a probe_pids=()

  pod_slot="$(acquire_parallel_slot "$parallel_root/pod-slots" "$max_parallel_pods")"
  while IFS=$'\t' read -r mode label target result_file; do
    ping_probe_task "$pod" "$mode" "$label" "$target" "$result_file" &
    probe_pids+=("$!")
    if ((${#probe_pids[@]} >= max_parallel_nfs_per_pod)); then
      for pid in "${probe_pids[@]}"; do
        wait "$pid" || true
      done
      probe_pids=()
    fi
  done < "$manifest"
  for pid in "${probe_pids[@]}"; do
    wait "$pid" || true
  done
  release_parallel_slot "$pod_slot"
}

phase_check_connectivity() {
  local pod
  local nf_name
  local target_ip
  local prober_mm_pod=''
  local manifest
  local result_file
  local result_index
  local -a gnodeb_targets
  local -a ipgw_pods
  local -a mm_pods
  local -a nf_names=(AUSF UDM SMF AMF_SELF AMF_REMOTE NRF EIR NSSF)
  local -a target_ips
  local ping_dir="$parallel_root/ping"
  local n2_dir="$ping_dir/3.1"
  local comm_dir="$ping_dir/3.2"
  local gtp_dir="$ping_dir/3.3"
  local peer_dir="$ping_dir/3.4"
  declare -A nf_targets=(
    [AUSF]="$ip_ausf"
    [UDM]="$ip_udm"
    [SMF]="$ip_smf"
    [AMF_SELF]="$ip_amf_self"
    [AMF_REMOTE]="$ip_amf_remote"
    [NRF]="$ip_nrf"
    [EIR]="$ip_eir"
    [NSSF]="$ip_nssf"
  )

  mkdir -p "$n2_dir" "$comm_dir" "$gtp_dir" "$peer_dir"
  heading '[PHASE 3] VERIFYING NETWORK CONNECTIVITY (PING TESTS)'
  mapfile -t mm_pods < <(get_running_pods mm)
  mapfile -t ipgw_pods < <(get_running_pods ipgw)
  read -r -a gnodeb_targets <<< "${ip_gnodeb//,/ }"
  if ((${#mm_pods[@]} > 0)); then
    prober_mm_pod="${mm_pods[0]}"
  fi

  result_index=0
  if ((${#ipgw_pods[@]} == 0)); then
    printf -v result_file '%s/%06d.result' "$n2_dir" "$result_index"
    write_probe_result "$result_file" FAIL 'No running IPGW pod found'
  else
    for pod in "${ipgw_pods[@]}"; do
      manifest="$parallel_root/ping-n2-$result_index.manifest"
      : > "$manifest"
      for target_ip in "${gnodeb_targets[@]}"; do
        printf -v result_file '%s/%06d.result' "$n2_dir" "$result_index"
        printf 'gnodeb\tN2\t%s\t%s\n' "$target_ip" "$result_file" >> "$manifest"
        result_index=$((result_index + 1))
      done
      launch_background run_ping_pod_worker "$pod" "$manifest"
    done
  fi
  [[ "$parallel_interfaces" == true ]] || wait_for_background

  if [[ -z "$prober_mm_pod" ]]; then
    write_probe_result "$comm_dir/000000.result" FAIL 'No running MM or MM Controller pod found to act as prober'
  else
    manifest="$parallel_root/ping-comm.manifest"
    printf 'comm\tCOMM\t%s\t%s\n' "$vip_comm" "$comm_dir/000000.result" > "$manifest"
    launch_background run_ping_pod_worker "$prober_mm_pod" "$manifest"
  fi
  [[ "$parallel_interfaces" == true ]] || wait_for_background

  if [[ -z "$prober_mm_pod" ]]; then
    write_probe_result "$gtp_dir/000000.result" FAIL 'No running MM or MM Controller pod found to act as prober'
  else
    manifest="$parallel_root/ping-gtp.manifest"
    printf 'gtp\tGTP\t%s\t%s\n' "$vip_gtp" "$gtp_dir/000000.result" > "$manifest"
    launch_background run_ping_pod_worker "$prober_mm_pod" "$manifest"
  fi
  [[ "$parallel_interfaces" == true ]] || wait_for_background

  result_index=0
  if ((${#mm_pods[@]} == 0)); then
    write_probe_result "$peer_dir/000000.result" FAIL 'No running MM or MM Controller pods found for peer NF checks'
  else
    for pod in "${mm_pods[@]}"; do
      manifest="$parallel_root/ping-peer-$result_index.manifest"
      : > "$manifest"
      for nf_name in "${nf_names[@]}"; do
        target_ips=()
        read -r -a target_ips <<< "${nf_targets[$nf_name]//,/ }"
        for target_ip in "${target_ips[@]}"; do
          printf -v result_file '%s/%06d.result' "$peer_dir" "$result_index"
          printf 'peer\t%s\t%s\t%s\n' "$nf_name" "$target_ip" "$result_file" >> "$manifest"
          result_index=$((result_index + 1))
        done
      done
      launch_background run_ping_pod_worker "$pod" "$manifest"
    done
  fi
  wait_for_background

  printf '\n%s 3.1 N2: IPGW VIP -> gNodeB%s\n' "$yellow" "$reset"
  render_probe_results "$n2_dir"
  printf '\n%s 3.2 SBI-Server: MM/MM Controller pod -> COMM VIP%s\n' "$yellow" "$reset"
  render_probe_results "$comm_dir"
  printf '\n%s 3.3 N26: MM/MM Controller pod -> GTP VIP%s\n' "$yellow" "$reset"
  render_probe_results "$gtp_dir"
  printf '\n%s 3.4 SBI-Client: MM/MM Controller pods -> peer NFs%s\n' "$yellow" "$reset"
  render_probe_results "$peer_dir"
}

url_for_log() {
  local value="$1"
  value="${value%%\?*}"
  value="${value%%#*}"
  printf '%s' "$value"
}

curl_probe_task() {
  local pod="$1"
  local nf_name="$2"
  local url="$3"
  local result_file="$4"
  local response
  local http_code
  local status
  local detail
  local display_url
  local job_slot
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
  job_slot="$(acquire_parallel_slot "$parallel_root/job-slots" "$max_total_parallel_jobs")"
  response="$(kubectl exec -n "$namespace" "$pod" -- "${curl_args[@]}" "$url" 2>&1)"
  status=$?
  http_code="${response##*$'\n'}"
  display_url="$(url_for_log "$url")"

  if ((status == 0)) && [[ "$http_code" =~ ^[1-5][0-9][0-9]$ ]]; then
    write_probe_result "$result_file" PASS "Pod [$pod]: NF [$nf_name] $display_url is reachable (HTTP $http_code)"
    release_parallel_slot "$job_slot"
    return
  fi

  detail="${response//$'\r'/ }"
  detail="${detail//$'\n'/ }"
  detail="${detail:0:240}"
  [[ -n "$detail" ]] || detail="curl exited with status $status"
  write_probe_result "$result_file" FAIL "Pod [$pod]: NF [$nf_name] $display_url is unreachable ($detail)"
  release_parallel_slot "$job_slot"
}

run_curl_pod_worker() {
  local pod="$1"
  local availability_result="$2"
  local manifest="$3"
  local nf_name
  local url
  local result_file
  local pid
  local pod_slot
  local job_slot
  local -a probe_pids=()

  pod_slot="$(acquire_parallel_slot "$parallel_root/pod-slots" "$max_parallel_pods")"
  job_slot="$(acquire_parallel_slot "$parallel_root/job-slots" "$max_total_parallel_jobs")"
  if ! kubectl exec -n "$namespace" "$pod" -- curl --version >/dev/null 2>&1; then
    write_probe_result "$availability_result" FAIL "Pod [$pod]: curl is not available in the selected container"
    release_parallel_slot "$job_slot"
    release_parallel_slot "$pod_slot"
    return
  fi
  release_parallel_slot "$job_slot"

  while IFS=$'\t' read -r nf_name url result_file; do
    curl_probe_task "$pod" "$nf_name" "$url" "$result_file" &
    probe_pids+=("$!")
    if ((${#probe_pids[@]} >= max_parallel_nfs_per_pod)); then
      for pid in "${probe_pids[@]}"; do
        wait "$pid" || true
      done
      probe_pids=()
    fi
  done < "$manifest"
  for pid in "${probe_pids[@]}"; do
    wait "$pid" || true
  done
  release_parallel_slot "$pod_slot"
}

phase_check_http() {
  local pod
  local nf_name
  local url
  local manifest
  local result_file
  local availability_result
  local result_index=0
  local -a mm_pods
  local -a target_urls
  local -a nf_names=(AUSF UDM SMF AMF_SELF AMF_REMOTE NRF EIR NSSF)
  declare -A nf_urls=(
    [AUSF]="$curl_ausf_urls"
    [UDM]="$curl_udm_urls"
    [SMF]="$curl_smf_urls"
    [AMF_SELF]="$curl_amf_self_urls"
    [AMF_REMOTE]="$curl_amf_remote_urls"
    [NRF]="$curl_nrf_urls"
    [EIR]="$curl_eir_urls"
    [NSSF]="$curl_nssf_urls"
  )

  heading '[PHASE 4] VERIFYING PEER NF HTTP CONNECTIVITY (CURL)'
  mapfile -t mm_pods < <(get_running_pods mm)
  local curl_dir="$parallel_root/curl"
  mkdir -p "$curl_dir"
  if ((${#mm_pods[@]} == 0)); then
    write_probe_result "$curl_dir/000000.result" FAIL 'No running MM or MM Controller pods found for peer NF curl checks'
    render_probe_results "$curl_dir"
    return
  fi

  for pod in "${mm_pods[@]}"; do
    printf -v availability_result '%s/%06d.result' "$curl_dir" "$result_index"
    result_index=$((result_index + 1))
    manifest="$parallel_root/curl-$result_index.manifest"
    : > "$manifest"
    for nf_name in "${nf_names[@]}"; do
      target_urls=()
      read -r -a target_urls <<< "${nf_urls[$nf_name]//,/ }"
      for url in "${target_urls[@]}"; do
        printf -v result_file '%s/%06d.result' "$curl_dir" "$result_index"
        printf '%s\t%s\t%s\n' "$nf_name" "$url" "$result_file" >> "$manifest"
        result_index=$((result_index + 1))
      done
    done
    launch_background run_curl_pod_worker "$pod" "$availability_result" "$manifest"
  done
  wait_for_background
  render_probe_results "$curl_dir"
}

run_checklist() {
  parallel_runtime_init || return

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
    printf 'Usage: %s {check|apply|verify|rollback} [key=value ...]\n' "$0" >&2
    exit 2
    ;;
esac
