#!/usr/bin/env bash
set -uo pipefail

action="${1:-}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-pramf01}"
expected_services="${SYSSETUP_PARAM_SERVICES:-aerospike comm-sbi dns dns-http ebm-svc-udp gtp-http mm mm-controller-external mm-controller-internal netconf nm-loadbalancer-svc nm-postgres nm-postgres-config nm-postgres-repl redis redis-sentinel sctp-http sctp-sctp-field}"
tcp_probe="${SYSSETUP_PARAM_TCP_PROBE:-true}"
probe_targets="${SYSSETUP_PARAM_PROBE_TARGETS:-all}"
connect_timeout="${SYSSETUP_PARAM_CONNECT_TIMEOUT:-3}"

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

checks=0
failures=0
tcp_checks=0
services=()
tcp_client=''

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

skip() {
  printf '    %s[SKIP]%s %s\n' "$yellow" "$reset" "$1"
}

validate_configuration() {
  local service
  declare -A seen=()

  if [[ ${#namespace} -gt 63 || ! "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
    printf 'Invalid Kubernetes namespace %q\n' "$namespace" >&2
    return 2
  fi
  case "$tcp_probe" in
    true|false) ;;
    *)
      printf 'Invalid tcp_probe value %q; expected true or false\n' "$tcp_probe" >&2
      return 2
      ;;
  esac
  case "$probe_targets" in
    cluster|external|all) ;;
    *)
      printf 'Invalid probe_targets value %q; expected cluster, external or all\n' "$probe_targets" >&2
      return 2
      ;;
  esac
  if [[ ! "$connect_timeout" =~ ^[0-9]+$ ]] || \
     ((10#$connect_timeout < 1 || 10#$connect_timeout > 60)); then
    printf 'Invalid connect_timeout %q; expected 1 through 60 seconds\n' "$connect_timeout" >&2
    return 2
  fi

  read -r -a services <<< "${expected_services//,/ }"
  if ((${#services[@]} == 0)); then
    printf 'Service list must not be empty\n' >&2
    return 2
  fi
  for service in "${services[@]}"; do
    if [[ ${#service} -gt 63 || ! "$service" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
      printf 'Invalid Kubernetes service name %q\n' "$service" >&2
      return 2
    fi
    if [[ -n "${seen[$service]:-}" ]]; then
      printf 'Duplicate Kubernetes service name %q\n' "$service" >&2
      return 2
    fi
    seen[$service]=1
  done
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
  if [[ "$tcp_probe" == true ]]; then
    if command -v curl >/dev/null 2>&1 && \
       curl --version 2>/dev/null | grep -Eqi 'Protocols:.*[[:space:]]telnet([[:space:]]|$)'; then
      tcp_client='curl'
    elif command -v telnet >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
      tcp_client='telnet'
    else
      printf 'TCP probing requires curl with telnet protocol support, or both telnet and timeout\n' >&2
      return 1
    fi
  fi
}

probe_with_curl() {
  local host="$1"
  local port="$2"
  local output
  local status

  output="$(curl --silent --show-error --verbose --output /dev/null \
    --noproxy '*' --connect-timeout "$connect_timeout" --max-time "$connect_timeout" \
    "telnet://${host}:${port}" </dev/null 2>&1)"
  status=$?
  if ((status == 0)) || grep -Eq "Connected to .* port ${port}" <<< "$output"; then
    return 0
  fi
  return 1
}

probe_with_telnet() {
  local host="$1"
  local port="$2"
  local output
  local status

  output="$(timeout "${connect_timeout}s" telnet "$host" "$port" </dev/null 2>&1)"
  status=$?
  if ((status == 0)) || grep -Eqi 'Connected to|Escape character is' <<< "$output"; then
    return 0
  fi
  return 1
}

probe_tcp_target() {
  local service="$1"
  local host="$2"
  local port="$3"

  tcp_checks=$((tcp_checks + 1))
  if [[ "$tcp_client" == curl ]]; then
    if probe_with_curl "$host" "$port"; then
      pass "Service [$service]: TCP $host:$port is reachable (curl)"
    else
      fail "Service [$service]: TCP $host:$port is unreachable (curl)"
    fi
  elif probe_with_telnet "$host" "$port"; then
    pass "Service [$service]: TCP $host:$port is reachable (telnet)"
  else
    fail "Service [$service]: TCP $host:$port is unreachable (telnet)"
  fi
}

probe_headless_service() {
  local service="$1"
  local endpoint_data
  local line
  local protocol
  local port
  local address
  local -a endpoint_lines
  local -a endpoint_addresses=()
  local -a endpoint_ports=()
  local -a line_addresses
  declare -A seen_addresses=()
  declare -A seen_ports=()

  if ! endpoint_data="$(kubectl get endpoints "$service" -n "$namespace" -o \
    'jsonpath={range .subsets[*]}{range .addresses[*]}{.ip}{"|"}{end}{"\n"}{range .ports[*]}{.protocol}{"|"}{.port}{"\n"}{end}{end}' 2>/dev/null)"; then
    fail "Service [$service]: endpoints could not be read"
    return
  fi
  mapfile -t endpoint_lines <<< "$endpoint_data"
  for line in "${endpoint_lines[@]}"; do
    [[ -z "$line" ]] && continue
    if [[ "$line" == *'|' ]]; then
      line_addresses=()
      IFS='|' read -r -a line_addresses <<< "$line"
      for address in "${line_addresses[@]}"; do
        [[ -z "$address" || -n "${seen_addresses[$address]:-}" ]] && continue
        seen_addresses[$address]=1
        endpoint_addresses+=("$address")
      done
      continue
    fi
    IFS='|' read -r protocol port <<< "$line"
    if [[ "$protocol" == TCP && "$port" =~ ^[0-9]+$ && -z "${seen_ports[$port]:-}" ]]; then
      seen_ports[$port]=1
      endpoint_ports+=("$port")
    fi
  done

  if ((${#endpoint_addresses[@]} == 0)); then
    fail "Service [$service]: no ready endpoint address was found"
    return
  fi
  if ((${#endpoint_ports[@]} == 0)); then
    fail "Service [$service]: no TCP endpoint port was found"
    return
  fi
  for address in "${endpoint_addresses[@]}"; do
    for port in "${endpoint_ports[@]}"; do
      probe_tcp_target "$service" "$address" "$port"
    done
  done
}

check_service() {
  local service="$1"
  local service_data
  local cluster_ip
  local external_data
  local external_label
  local address
  local line
  local protocol
  local port
  local -a service_lines
  local -a tcp_ports=()
  local -a external_addresses=()
  local -a probe_addresses=()
  declare -A seen_addresses=()

  if ! service_data="$(kubectl get service "$service" -n "$namespace" -o \
    'jsonpath={.spec.clusterIP}{"\n"}{range .spec.externalIPs[*]}{.}{"|"}{end}{range .status.loadBalancer.ingress[*]}{.ip}{.hostname}{"|"}{end}{"\n"}{range .spec.ports[*]}{.protocol}{"|"}{.port}{"\n"}{end}' 2>/dev/null)"; then
    fail "Service [$service] is missing from namespace [$namespace]"
    return
  fi
  mapfile -t service_lines <<< "$service_data"
  cluster_ip="${service_lines[0]:-None}"
  external_data="${service_lines[1]:-}"
  IFS='|' read -r -a external_addresses <<< "$external_data"
  external_label="${external_data%|}"
  [[ -n "$external_label" ]] || external_label='None'
  pass "Service [$service] exists (ClusterIP: ${cluster_ip:-None}, External: $external_label)"

  if [[ "$tcp_probe" != true ]]; then
    return
  fi
  for line in "${service_lines[@]:2}"; do
    [[ -z "$line" ]] && continue
    IFS='|' read -r protocol port <<< "$line"
    if [[ "$protocol" == TCP && "$port" =~ ^[0-9]+$ ]]; then
      tcp_ports+=("$port")
    fi
  done
  if ((${#tcp_ports[@]} == 0)); then
    skip "Service [$service] has no TCP port"
    return
  fi

  if [[ "$probe_targets" != external && -n "$cluster_ip" && "$cluster_ip" != None ]]; then
    seen_addresses[$cluster_ip]=1
    probe_addresses+=("$cluster_ip")
  fi
  if [[ "$probe_targets" != cluster ]]; then
    for address in "${external_addresses[@]}"; do
      [[ -z "$address" || -n "${seen_addresses[$address]:-}" ]] && continue
      seen_addresses[$address]=1
      probe_addresses+=("$address")
    done
  fi

  if ((${#probe_addresses[@]} == 0)) && \
     [[ "$probe_targets" != external && ( -z "$cluster_ip" || "$cluster_ip" == None ) ]]; then
    probe_headless_service "$service"
    return
  fi
  if ((${#probe_addresses[@]} == 0)); then
    skip "Service [$service] has no $probe_targets TCP target IP"
    return
  fi
  for address in "${probe_addresses[@]}"; do
    for port in "${tcp_ports[@]}"; do
      probe_tcp_target "$service" "$address" "$port"
    done
  done
}

run_service_checks() {
  local service

  heading "KUBERNETES SERVICE CHECK: namespace [$namespace]"
  for service in "${services[@]}"; do
    printf '\n  Checking service: %s%s%s\n' "$yellow" "$service" "$reset"
    check_service "$service"
  done

  printf '\nService check completed: expected=%d tcp_probes=%d checks=%d failures=%d.\n' \
    "${#services[@]}" "$tcp_checks" "$checks" "$failures"
  ((failures == 0))
}

case "$action" in
  check)
    preflight || exit $?
    # This audit must execute on every run.
    exit 10
    ;;
  apply)
    # Read-only feature; no system state is changed.
    :
    ;;
  verify)
    preflight && run_service_checks
    ;;
  rollback)
    # Read-only feature; no rollback is required.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
