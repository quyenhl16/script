#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-}"
container="${SYSSETUP_PARAM_CONTAINER:-}"
confd_dir="${SYSSETUP_PARAM_CONFD_DIR:-.}"
source_config_file="${SYSSETUP_PARAM_SOURCE_CONFIG_FILE:-}"
destination_config_file="${SYSSETUP_PARAM_DESTINATION_CONFIG_FILE:-config.xml}"
pod_candidates=(netconf-0 netconf-1)
selected_master=""

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 2
}

preflight() {
  command -v kubectl >/dev/null 2>&1 || fail "kubectl command was not found"
  [[ -n "$namespace" ]] || fail "namespace parameter is required"
  [[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || fail "invalid Kubernetes namespace: $namespace"
  (( ${#namespace} <= 63 )) || fail "Kubernetes namespace is longer than 63 characters"
  [[ -n "$confd_dir" && "$confd_dir" != *$'\n'* && "$confd_dir" != *$'\r'* ]] || fail "invalid confd_dir"
  [[ -n "$source_config_file" ]] || fail "source_config_file parameter is required"
  [[ -f "$source_config_file" && -r "$source_config_file" ]] || fail "source config is not a readable regular file: $source_config_file"
  [[ -s "$source_config_file" ]] || fail "source config file is empty: $source_config_file"
  [[ "$source_config_file" != *$'\n'* && "$source_config_file" != *$'\r'* ]] || fail "invalid source_config_file"
  [[ "$destination_config_file" =~ ^config[A-Za-z0-9._-]*\.xml$ ]] || fail "destination_config_file must be a config*.xml filename"
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

check_destination() {
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
  ' sh "$confd_dir" 2>&1)"; then
    fail "$selected_master: $output"
  fi
  printf '[PASS] %s: ConfD directory is ready: %s\n' "$selected_master" "$confd_dir"
}

copy_config() {
  local output
  if ! output="$(kubectl_exec "$selected_master" sh -c '
    set -eu
    cd "$1" || exit 20
    umask 077
    temporary=".syssetup-$2.tmp.$$"
    trap '\''rm -f "$temporary"'\'' EXIT HUP INT TERM
    cat > "$temporary"
    test -s "$temporary" || {
      printf "copied configuration is empty\n" >&2
      exit 23
    }
    mv -f "$temporary" "$2"
    trap - EXIT HUP INT TERM
    test -f "$2" && test -r "$2"
  ' sh "$confd_dir" "$destination_config_file" < "$source_config_file" 2>&1)"; then
    fail "$selected_master: cannot copy $source_config_file to $confd_dir/$destination_config_file: $output"
  fi
  printf '[PASS] copied %s to %s:%s/%s\n' "$source_config_file" "$selected_master" "$confd_dir" "$destination_config_file"
}

run_load_attempt() {
  {
    printf 'config\n'
    printf 'load merge %s\n' "$destination_config_file"
    printf 'commit\n'
    printf 'exit\n'
    printf 'exit\n'
  } | kubectl_exec "$selected_master" sh -c '
    cd "$1" || exit 20
    exec ./run_cli.sh -N -s
  ' sh "$confd_dir"
}

load_and_commit() {
  local attempt output status
  for attempt in 1 2; do
    printf 'Loading configuration on %s (attempt %d/2)...\n' "$selected_master" "$attempt"
    if output="$(run_load_attempt 2>&1)"; then
      [[ -z "$output" ]] || printf '%s\n' "$output"
      printf '[PASS] %s: load merge and commit completed\n' "$selected_master"
      return 0
    else
      status=$?
      printf '[WARN] %s: load/commit attempt %d failed (exit=%d)\n' "$selected_master" "$attempt" "$status" >&2
      [[ -z "$output" ]] || printf '%s\n' "$output" >&2
    fi
    if (( attempt == 1 )); then
      printf 'Retrying the complete load and commit transaction...\n' >&2
      sleep 2
    fi
  done
  fail "$selected_master: load merge or commit failed after 2 attempts"
}

case "$action" in
  check)
    preflight
    select_master
    check_destination
    # This operation must run whenever the feature is selected.
    exit 10
    ;;
  apply)
    preflight
    select_master
    check_destination
    copy_config
    load_and_commit
    ;;
  verify)
    preflight
    select_master
    printf '[PASS] %s remains the only ConfD HA master after commit\n' "$selected_master"
    ;;
  rollback)
    # ConfD owns rollback semantics; this feature does not issue an implicit rollback.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
