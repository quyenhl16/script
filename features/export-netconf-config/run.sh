#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-}"
container="${SYSSETUP_PARAM_CONTAINER:-}"
confd_dir="${SYSSETUP_PARAM_CONFD_DIR:-.}"
remote_config_file="${SYSSETUP_PARAM_REMOTE_CONFIG_FILE:-amf-running-config.xml}"
destination_config_file="${SYSSETUP_PARAM_DESTINATION_CONFIG_FILE:-amf-running-config.xml}"
overwrite="${SYSSETUP_PARAM_OVERWRITE:-false}"
pod_candidates=(netconf-0 netconf-1)
selected_master=""
temporary_dir=""

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 2
}

validate_configuration() {
  command -v kubectl >/dev/null 2>&1 || fail "kubectl command was not found"
  [[ -n "$namespace" ]] || fail "namespace parameter is required"
  [[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || fail "invalid Kubernetes namespace: $namespace"
  (( ${#namespace} <= 63 )) || fail "Kubernetes namespace is longer than 63 characters"
  [[ -n "$confd_dir" && "$confd_dir" != *$'\n'* && "$confd_dir" != *$'\r'* && "$confd_dir" != *:* ]] || fail "invalid confd_dir"
  [[ "$remote_config_file" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*\.xml$ ]] || fail "remote_config_file must be an XML filename"
  [[ -n "$destination_config_file" ]] || fail "destination_config_file parameter is required"
  [[ "$destination_config_file" != *$'\n'* && "$destination_config_file" != *$'\r'* ]] || fail "invalid destination_config_file"
  [[ "${destination_config_file,,}" == *.xml ]] || fail "destination_config_file must use the .xml extension"
  [[ "$overwrite" == true || "$overwrite" == false ]] || fail "overwrite must be true or false"
  if [[ -n "$container" ]]; then
    [[ "$container" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || fail "invalid container name: $container"
  fi
}

kubectl_exec() {
  local pod="$1"
  shift
  local -a arguments=(-n "$namespace" exec -i "$pod")
  if [[ -n "$container" ]]; then
    arguments+=(-c "$container")
  fi
  kubectl "${arguments[@]}" -- "$@"
}

pod_phase() {
  kubectl -n "$namespace" get pod "$1" -o 'jsonpath={.status.phase}' 2>/dev/null
}

query_ha() {
  local pod="$1"
  printf 'show confd-state ha\nexit\n' | kubectl_exec "$pod" sh -c '
    cd "$1" || exit 20
    test -x ./run_cli.sh || {
      printf "run_cli.sh is missing or not executable in %s\n" "$1" >&2
      exit 21
    }
    exec ./run_cli.sh -N -s
  ' sh "$confd_dir"
}

select_master() {
  local pod phase output normalized
  local -a masters=()

  for pod in "${pod_candidates[@]}"; do
    phase="$(pod_phase "$pod" || true)"
    if [[ "$phase" != "Running" ]]; then
      printf '[SKIP] %s: pod is unavailable or not Running (phase=%s)\n' "$pod" "${phase:-unknown}"
      continue
    fi
    if ! output="$(query_ha "$pod" 2>&1)"; then
      printf '[WARN] %s: cannot query ConfD HA state\n%s\n' "$pod" "$output" >&2
      continue
    fi
    normalized="$(tr -d '\r' <<< "$output")"
    if grep -Eq '^[[:space:]]*confd-state ha mode master[[:space:]]*$' <<< "$normalized"; then
      printf '[PASS] %s: ConfD HA mode is master\n' "$pod"
      masters+=("$pod")
    elif grep -Eq '^[[:space:]]*confd-state ha mode ' <<< "$normalized"; then
      printf '[SKIP] %s: ConfD HA mode is not master\n' "$pod"
    else
      printf '[WARN] %s: ConfD HA output does not contain a mode\n%s\n' "$pod" "$normalized" >&2
    fi
  done

  (( ${#masters[@]} > 0 )) || fail "neither netconf-0 nor netconf-1 reports ConfD HA mode master"
  (( ${#masters[@]} == 1 )) || fail "multiple NetConf pods report ConfD HA mode master: ${masters[*]}"
  selected_master="${masters[0]}"
}

check_export_target() {
  local destination_dir
  destination_dir="$(dirname -- "$destination_config_file")"
  [[ -d "$destination_dir" ]] || fail "local destination directory does not exist: $destination_dir"
  [[ -w "$destination_dir" ]] || fail "local destination directory is not writable: $destination_dir"
  [[ ! -d "$destination_config_file" ]] || fail "local destination is a directory: $destination_config_file"
  if [[ -e "$destination_config_file" && "$overwrite" != true ]]; then
    fail "local destination already exists; set overwrite=true to replace it: $destination_config_file"
  fi

  local output
  if ! output="$(kubectl_exec "$selected_master" sh -c '
    cd "$1" || exit 20
    test -x ./run_cli.sh || {
      printf "run_cli.sh is missing or not executable in %s\n" "$1" >&2
      exit 21
    }
    test -w . || {
      printf "ConfD directory is not writable: %s\n" "$1" >&2
      exit 22
    }
    command -v tar >/dev/null 2>&1 || {
      printf "tar is required in the container for kubectl cp\n" >&2
      exit 23
    }
  ' sh "$confd_dir" 2>&1)"; then
    fail "$selected_master: $output"
  fi
  printf '[PASS] export target is ready: %s:%s -> %s\n' "$selected_master" "$confd_dir" "$destination_config_file"
}

remove_remote_export() {
  [[ -n "$selected_master" ]] || return 0
  kubectl_exec "$selected_master" sh -c 'cd "$1" && rm -f -- "$2"' sh "$confd_dir" "$remote_config_file" >/dev/null 2>&1 || true
}

cleanup_export() {
  if [[ -n "$temporary_dir" && -d "$temporary_dir" ]]; then
    rm -rf -- "$temporary_dir" || true
  fi
  remove_remote_export
}

export_running_config() {
  local output
  remove_remote_export
  if ! output="$({
    printf 'show running-config amf | display xml | save %s\n' "$remote_config_file"
    printf 'exit\n'
  } | kubectl_exec "$selected_master" sh -c '
    cd "$1" || exit 20
    exec ./run_cli.sh -N -s
  ' sh "$confd_dir" 2>&1)"; then
    fail "$selected_master: exporting the AMF running configuration failed: $output"
  fi
  [[ -z "$output" ]] || printf '%s\n' "$output"
  if ! kubectl_exec "$selected_master" sh -c 'cd "$1" && test -s "$2"' sh "$confd_dir" "$remote_config_file"; then
    fail "$selected_master: exported file is missing or empty: $confd_dir/$remote_config_file"
  fi
  printf '[PASS] exported running-config amf to %s:%s/%s\n' "$selected_master" "$confd_dir" "$remote_config_file"
}

copy_export_local() {
  local destination_dir temporary_file remote_path
  destination_dir="$(dirname -- "$destination_config_file")"
  temporary_dir="$(mktemp -d "$destination_dir/.syssetup-netconf-export.XXXXXX")"
  temporary_file="$temporary_dir/$remote_config_file"
  remote_path="$confd_dir/$remote_config_file"
  trap cleanup_export EXIT

  local -a arguments=(-n "$namespace" cp "$selected_master:$remote_path" "$temporary_file")
  if [[ -n "$container" ]]; then
    arguments+=(-c "$container")
  fi
  kubectl "${arguments[@]}" || fail "cannot copy $selected_master:$remote_path to $destination_config_file"
  [[ -s "$temporary_file" ]] || fail "copied XML configuration is empty"
  grep -Eq '<[^>]+>' "$temporary_file" || fail "copied file does not appear to contain XML"
  chmod 0600 "$temporary_file"
  mv -f -- "$temporary_file" "$destination_config_file"
  cleanup_export
  trap - EXIT
  printf '[PASS] copied exported configuration to %s\n' "$destination_config_file"
}

verify_local_export() {
  [[ -f "$destination_config_file" && -r "$destination_config_file" && -s "$destination_config_file" ]] || fail "exported local XML file is missing, unreadable or empty: $destination_config_file"
  grep -Eq '<[^>]+>' "$destination_config_file" || fail "exported local file does not appear to contain XML: $destination_config_file"
  printf '[PASS] local export is ready: %s\n' "$destination_config_file"
}

case "$action" in
  check)
    validate_configuration
    select_master
    check_export_target
    # Export is an explicit snapshot operation and must run when selected.
    exit 10
    ;;
  apply)
    validate_configuration
    select_master
    check_export_target
    export_running_config
    copy_export_local
    ;;
  verify)
    validate_configuration
    verify_local_export
    ;;
  rollback)
    # Exporting is read-only; an existing local file is not removed implicitly.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
