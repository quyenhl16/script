#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 2
}

action="${1:-}"
xml_file="${SYSSETUP_PARAM_XML_FILE:-}"
rules_file="${SYSSETUP_PARAM_RULES_FILE:-checks/xml/system-critical-paths.json}"

preflight() {
  [[ -n "$xml_file" ]] || fail "xml_file parameter is required"
  [[ -f "$xml_file" && -r "$xml_file" ]] || fail "XML file is not a readable regular file: $xml_file"
  [[ -n "$rules_file" ]] || fail "rules_file parameter is required"
  [[ -f "$rules_file" && -r "$rules_file" ]] || fail "rules file is not a readable regular file: $rules_file"
  command -v python3 >/dev/null 2>&1 || fail "python3 command was not found"
  command -v xmllint >/dev/null 2>&1 || fail "xmllint command was not found; install libxml2"
}

verify_config() {
python3 - "$rules_file" "$xml_file" <<'PY'
import ipaddress
import json
import os
import re
import subprocess
import sys

MAX_INPUT_SIZE = 50 * 1024 * 1024
SUPPORTED_COMPARISONS = {"exact", "trimmed", "integer", "boolean", "ip", "regex"}


class InputError(Exception):
    pass


def read_rules(path):
    try:
        if not os.path.isfile(path):
            raise InputError(f"rules path is not a regular file: {path}")
        if os.path.getsize(path) > MAX_INPUT_SIZE:
            raise InputError("rules file is larger than 50 MiB")
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise InputError(f"cannot read rules: {error}") from error
    if not isinstance(data, dict):
        raise InputError("rules root must be a JSON object")
    if data.get("apiVersion") != "syssetup/xml-check/v1":
        raise InputError(f"unsupported rules apiVersion: {data.get('apiVersion')!r}")
    checks = data.get("checks")
    if not isinstance(checks, list) or not checks:
        raise InputError("rules.checks must be a non-empty array")
    return data, checks


def run_xmllint(xml_path, expression=None):
    command = ["xmllint", "--nonet"]
    if expression is None:
        command.extend(["--noout", xml_path])
    else:
        command.extend(["--xpath", expression, xml_path])
    return subprocess.run(command, text=True, capture_output=True, check=False)


def selected_node_count(xml_path, xpath):
    result = run_xmllint(xml_path, f"count({xpath})")
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip() or "invalid XPath"
        raise InputError(detail)
    try:
        return int(float(result.stdout.strip()))
    except ValueError as error:
        raise InputError(f"XPath must select nodes, got count {result.stdout.strip()!r}") from error


def selected_value(xml_path, xpath):
    result = run_xmllint(xml_path, f"string({xpath})")
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip() or "cannot read XPath value"
        raise InputError(detail)
    return result.stdout


def canonical_boolean(value):
    normalized = value.strip().lower()
    if normalized in {"true", "1", "yes", "on"}:
        return True
    if normalized in {"false", "0", "no", "off"}:
        return False
    raise ValueError(f"not a boolean: {value!r}")


def compare(actual, expected, mode):
    expected_text = str(expected)
    if mode == "exact":
        return actual == expected_text
    if mode == "trimmed":
        return actual.strip() == expected_text.strip()
    if mode == "integer":
        return int(actual.strip()) == int(expected_text.strip())
    if mode == "boolean":
        return canonical_boolean(actual) == canonical_boolean(expected_text)
    if mode == "ip":
        return ipaddress.ip_address(actual.strip()) == ipaddress.ip_address(expected_text.strip())
    if mode == "regex":
        return re.fullmatch(expected_text, actual) is not None
    raise ValueError(f"unsupported comparison: {mode}")


def display(value, sensitive):
    return "<redacted>" if sensitive else repr(value)


def main():
    rules, checks = read_rules(sys.argv[1])
    xml_path = sys.argv[2].strip()
    if not xml_path:
        raise InputError("XML file path is required")
    if not os.path.isfile(xml_path) or not os.access(xml_path, os.R_OK):
        raise InputError(f"XML target is not a readable regular file: {xml_path}")
    if os.path.getsize(xml_path) > MAX_INPUT_SIZE:
        raise InputError("XML target is larger than 50 MiB")
    parsed = run_xmllint(xml_path)
    if parsed.returncode != 0:
        detail = parsed.stderr.strip() or "invalid XML document"
        raise InputError(detail)

    failures = 0
    seen_ids = set()
    for index, check in enumerate(checks, start=1):
        if not isinstance(check, dict):
            raise InputError(f"check {index} must be an object")
        check_id = str(check.get("id", "")).strip()
        xpath = str(check.get("xpath", "")).strip()
        if not check_id or not xpath or "expected" not in check:
            raise InputError(f"check {index} requires id, xpath and expected")
        if re.fullmatch(r"[A-Za-z][A-Za-z0-9._-]*", check_id) is None:
            raise InputError(f"check {index} has invalid id: {check_id!r}")
        if check_id in seen_ids:
            raise InputError(f"duplicate check id: {check_id}")
        seen_ids.add(check_id)
        mode = str(check.get("compare", "exact")).strip().lower()
        if mode not in SUPPORTED_COMPARISONS:
            raise InputError(f"check {check_id}: unsupported comparison {mode!r}")
        required = check.get("required", True)
        sensitive = check.get("sensitive", False)
        if not isinstance(required, bool) or not isinstance(sensitive, bool):
            raise InputError(f"check {check_id}: required and sensitive must be boolean")
        expected = check["expected"]
        if expected is None or isinstance(expected, (dict, list)):
            raise InputError(f"check {check_id}: expected must be a scalar value")

        try:
            count = selected_node_count(xml_path, xpath)
        except InputError as error:
            raise InputError(f"check {check_id}: {error}") from error
        if count == 0:
            if required:
                print(f"[FAIL] {check_id}: XPath did not select a node", file=sys.stderr)
                failures += 1
            else:
                print(f"[SKIP] {check_id}: optional XPath did not select a node")
            continue
        if count != 1:
            print(f"[FAIL] {check_id}: XPath selected {count} nodes; expected exactly one", file=sys.stderr)
            failures += 1
            continue

        actual = selected_value(xml_path, xpath)
        try:
            matched = compare(actual, expected, mode)
        except (ValueError, TypeError, re.error) as error:
            raise InputError(f"check {check_id}: invalid {mode} value: {error}") from error
        if matched:
            print(f"[PASS] {check_id}: value={display(actual, sensitive)}")
        else:
            print(
                f"[FAIL] {check_id}: expected={display(str(expected), sensitive)} "
                f"actual={display(actual, sensitive)} compare={mode}",
                file=sys.stderr,
            )
            failures += 1

    passed = len(checks) - failures
    print(f"Summary: total={len(checks)} passed_or_skipped={passed} failed={failures}")
    return 1 if failures else 0


try:
    sys.exit(main())
except InputError as error:
    print(f"Error: {error}", file=sys.stderr)
    sys.exit(2)
PY
}

case "$action" in
  check)
    preflight
    # Verification is read-only but must run on every invocation.
    exit 10
    ;;
  apply)
    :
    ;;
  verify)
    preflight
    verify_config
    ;;
  rollback)
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
