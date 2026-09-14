#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"

if [[ -z "${NO_COLOR:-}" ]]; then
  bold=$'\033[1m'
  cyan=$'\033[1;36m'
  yellow=$'\033[1;33m'
  green=$'\033[0;32m'
  muted=$'\033[0;90m'
  reset=$'\033[0m'
else
  bold=''
  cyan=''
  yellow=''
  green=''
  muted=''
  reset=''
fi

print_title() {
  printf '%s%s\n' "$cyan" '======================================================================'
  printf '                     AMF SYSTEM VERIFICATION GUIDE\n'
  printf '%s%s\n\n' '======================================================================' "$reset"
}

print_section() {
  local number="$1"
  local title="$2"
  printf '%s%s%s\n' "$yellow" '----------------------------------------------------------------------' "$reset"
  printf '%s%s. %s%s\n' "$yellow" "$number" "$title" "$reset"
  printf '%s%s%s\n\n' "$yellow" '----------------------------------------------------------------------' "$reset"
}

print_item() {
  local index="$1"
  local title="$2"
  shift 2
  printf '%s%s%s %s%s\n' "$bold" "$index" "$reset" "$bold" "$title$reset"
  while (($# > 0)); do
    printf '    %s[ ]%s %s\n' "$green" "$reset" "$1"
    shift
  done
  printf '\n'
}

print_note() {
  printf '%sNote:%s %s\n' "$muted" "$reset" "$1"
}

show_guide() {
  print_title

  print_section 1 'BEFORE INSTALLING THE AMF CNF'
  printf '%sComplete the following prerequisites to install the AMF CNF successfully through MANO.%s\n\n' "$bold" "$reset"
  print_item '1.1' 'Enable SCTP on RHEL 8 or later servers' \
    'If an installation server runs RHEL 8 or later, verify that SCTP is enabled.' \
    'Use the enable-sctp feature to install, enable, load, and verify the SCTP kernel module.'
  print_item '1.2' 'Verify bond VLAN interfaces for OSPF' \
    'If the system uses OSPF, verify the required bond VLAN interfaces on every server.' \
    'Use the verify-network feature to confirm that each bond VLAN exists and is UP.' \
    'If a required bond VLAN does not exist, create it with the create-bond-vlan feature, then verify it again.'
  print_item '1.3' 'Create local paths for services that use persistent volumes' \
    'Use the create-local-path feature to prepare local paths on the target servers.' \
    'Create and verify paths for these PV-backed services: aerospike, netconfig, redis, ebm, nm, and nm-postgres.' \
    'Confirm the required owner, group, and permissions before starting the MANO installation.'

  print_section 2 'AFTER INSTALLING THE AMF CNF'
  printf '%sAfter the AMF system is up, perform the following checks.%s\n\n' "$bold" "$reset"
  print_item '2.1' 'Check environment variables' \
    'Use the k8s-env-check feature to compare workload environment variables with the approved baseline.' \
    'Review all missing, unexpected, and mismatched environment values.'
  print_item '2.2' 'Check workload resources' \
    'Use the k8s-resource-check feature to compare CPU and memory requests and limits with the approved baseline.' \
    'Review all missing workloads, unexpected workloads, and resource mismatches.'
  print_item '2.3' 'Check Kubernetes services' \
    'Use the k8s-service-check feature to verify required services, endpoints, external IPs, and TCP reachability.' \
    'Resolve every missing or unhealthy required service before continuing.'
  print_item '2.4' 'Check system connectivity' \
    'Use the k8s-connectivity-check feature to verify VIPs, OSPF, ping paths, and peer NF HTTP connectivity.' \
    'Review every configured NF target and investigate all failed paths.'
  print_item '2.5' 'Compare the running NetConf configuration with the reference XML' \
    'Use export-netconf-config to retrieve the AMF configuration currently running on the system.' \
    'Use compare-xml-config to compare the exported XML with the approved reference XML file.' \
    'Pay special attention to values under the configured criticalPaths and resolve every critical difference or missing required path.'

  print_section 3 'ADDITIONAL INFORMATION'
  print_item '3.1' 'Recommended feature sequence' \
    'Before installation: check-os, enable-sctp, verify-network, verify-xml-config.' \
    'After installation: k8s-env-check, k8s-resource-check, k8s-service-check, k8s-connectivity-check.' \
    'Finish with export-netconf-config and compare-xml-config.'
  print_item '3.2' 'Troubleshooting and safety' \
    'Review the first failed check before retrying dependent checks.' \
    'Do not change a production configuration without an approved rollback plan.' \
    'Redact credentials, tokens, and sensitive subscriber data from shared evidence.'
  print_item '3.3' 'Completion criteria' \
    'All mandatory checks pass or have an approved exception.' \
    'The exported running configuration and generated report are retained.' \
    'The installation result, exceptions, and follow-up actions are documented.'

  print_note 'This feature displays guidance only and does not modify the system.'
}

case "$action" in
  check)
    # Always continue so the guide is displayed during verify.
    exit 10
    ;;
  apply)
    # This feature is informational and has no apply operation.
    :
    ;;
  verify)
    show_guide
    ;;
  rollback)
    # No system state is changed.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
