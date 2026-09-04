#!/usr/bin/env python3
"""Validate alarm mapping data and send each payload through curl safely."""

from __future__ import print_function

import json
import os
import shutil
import subprocess
import sys
from urllib.parse import urlsplit


MAX_DATA_BYTES = 5 * 1024 * 1024
STATUS_MARKER = "__SYSSETUP_HTTP_STATUS__:"


class InputError(ValueError):
    pass


def load_requests(path):
    try:
        size = os.path.getsize(path)
    except OSError as exc:
        raise InputError("cannot inspect requests file: {0}".format(exc))
    if size > MAX_DATA_BYTES:
        raise InputError("requests file exceeds the 5 MiB limit")

    try:
        with open(path, "r", encoding="utf-8") as handle:
            document = json.load(handle)
    except (OSError, ValueError) as exc:
        raise InputError("cannot read requests JSON: {0}".format(exc))

    if not isinstance(document, dict):
        raise InputError("requests file root must be an object")
    if document.get("apiVersion") != "syssetup/alarm-mappings/v1":
        raise InputError("unsupported or missing apiVersion")
    requests = document.get("requests")
    if not isinstance(requests, list) or not requests:
        raise InputError("requests must be a non-empty array")

    names = set()
    validated = []
    for index, request in enumerate(requests, 1):
        if not isinstance(request, dict):
            raise InputError("request #{0} must be an object".format(index))
        name = request.get("name")
        payload = request.get("payload")
        if not isinstance(name, str) or not name.strip():
            raise InputError("request #{0} has an invalid name".format(index))
        if name in names:
            raise InputError("duplicate request name: {0}".format(name))
        names.add(name)
        if not isinstance(payload, list) or not payload:
            raise InputError("request {0} payload must be a non-empty array".format(name))
        phases = set()
        for item_index, item in enumerate(payload, 1):
            if not isinstance(item, dict):
                raise InputError("request {0} item #{1} must be an object".format(name, item_index))
            phase = item.get("PodPhase")
            pod_name = item.get("PodName")
            alarm_id = item.get("AlarmId")
            if not isinstance(phase, str) or not phase:
                raise InputError("request {0} item #{1} has an invalid PodPhase".format(name, item_index))
            if phase in phases:
                raise InputError("request {0} has duplicate PodPhase {1}".format(name, phase))
            phases.add(phase)
            if pod_name != name:
                raise InputError(
                    "request {0} item #{1} PodName must match the request name".format(name, item_index)
                )
            if isinstance(alarm_id, bool) or not isinstance(alarm_id, int):
                raise InputError("request {0} item #{1} has an invalid AlarmId".format(name, item_index))
        validated.append((name, payload))
    return validated


def validate_endpoint(endpoint):
    try:
        parsed = urlsplit(endpoint)
    except ValueError as exc:
        raise InputError("invalid endpoint: {0}".format(exc))
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        raise InputError("endpoint must be an absolute HTTP or HTTPS URL")
    if parsed.username is not None or parsed.password is not None:
        raise InputError("endpoint must not contain credentials")
    if parsed.fragment:
        raise InputError("endpoint must not contain a URL fragment")


def print_detail(label, value):
    value = value.strip()
    if not value:
        return
    for line in value.splitlines():
        print("  {0}: {1}".format(label, line))


def send_request(curl, endpoint, name, payload, connect_timeout, request_timeout):
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    marker = "\n" + STATUS_MARKER
    command = [
        curl,
        "--silent",
        "--show-error",
        "--request",
        "PUT",
        "--header",
        "Content-Type: application/json",
        "--data-binary",
        body,
        "--connect-timeout",
        str(connect_timeout),
        "--max-time",
        str(request_timeout),
        "--write-out",
        marker + "%{http_code}",
        endpoint,
    ]
    try:
        completed = subprocess.run(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
            timeout=connect_timeout + request_timeout + 5,
            check=False,
        )
    except subprocess.TimeoutExpired:
        print("[FAIL] {0}: curl process timed out".format(name))
        return False

    output, separator, status_text = completed.stdout.rpartition(marker)
    status_text = status_text.strip() if separator else ""
    http_ok = len(status_text) == 3 and status_text.isdigit() and 200 <= int(status_text) < 300
    success = completed.returncode == 0 and http_ok
    label = "PASS" if success else "FAIL"
    status = "HTTP {0}".format(status_text) if status_text else "no HTTP status"
    print("[{0}] {1}: {2}".format(label, name, status))
    print_detail("response", output)
    print_detail("curl", completed.stderr)
    return success


def main(argv):
    if len(argv) != 5:
        print("Usage: send.py REQUESTS_FILE ENDPOINT CONNECT_TIMEOUT REQUEST_TIMEOUT", file=sys.stderr)
        return 2

    requests_file, endpoint = argv[1], argv[2]
    try:
        connect_timeout = int(argv[3])
        request_timeout = int(argv[4])
        if connect_timeout <= 0 or request_timeout <= 0:
            raise ValueError
    except ValueError:
        print("Error: timeouts must be positive integers", file=sys.stderr)
        return 2

    try:
        validate_endpoint(endpoint)
        requests = load_requests(requests_file)
    except InputError as exc:
        print("Error: {0}".format(exc), file=sys.stderr)
        return 2

    curl = shutil.which("curl")
    if curl is None:
        print("Error: curl command was not found", file=sys.stderr)
        return 2

    passed = 0
    print("Alarm mapping endpoint: {0}".format(endpoint))
    print("Requests: {0}".format(len(requests)))
    for name, payload in requests:
        if send_request(curl, endpoint, name, payload, connect_timeout, request_timeout):
            passed += 1
    failed = len(requests) - passed
    label = "PASS" if failed == 0 else "FAIL"
    print("[{0}] Summary: total={1} passed={2} failed={3}".format(label, len(requests), passed, failed))
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
