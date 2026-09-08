#!/usr/bin/env python3
import argparse
import base64
import fnmatch
import json
import os
import posixpath
import re
import subprocess
import sys
import zipfile
import xml.etree.ElementTree as ET


MAX_XLSX_SIZE = 50 * 1024 * 1024
MAX_UNCOMPRESSED_SIZE = 250 * 1024 * 1024
MAX_ARCHIVE_ENTRIES = 10000
COMMAND_TIMEOUT = 60
MAX_DISPLAY_LENGTH = 256
ENV_NAME_PATTERN = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
CELL_REFERENCE_PATTERN = re.compile(r"^([A-Za-z]+)([0-9]+)$")
MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
PACKAGE_REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"


class InputError(Exception):
    pass


class KubernetesError(Exception):
    pass


def validate_file(path, label, maximum):
    try:
        if not os.path.isfile(path):
            raise InputError("{} is not a regular file: {}".format(label, path))
        size = os.path.getsize(path)
        if size > maximum:
            raise InputError("{} is larger than {} MiB".format(label, maximum // 1024 // 1024))
    except OSError as error:
        raise InputError("cannot inspect {}: {}".format(label, error)) from error


def read_file(path, label, maximum=MAX_XLSX_SIZE):
    validate_file(path, label, maximum)
    try:
        with open(path, "rb") as handle:
            return handle.read()
    except OSError as error:
        raise InputError("cannot read {}: {}".format(label, error)) from error


def safe_xml(content, label):
    upper = content.upper()
    if b"<!DOCTYPE" in upper or b"<!ENTITY" in upper:
        raise InputError("{} contains a DTD or entity declaration".format(label))
    try:
        return ET.fromstring(content)
    except ET.ParseError as error:
        raise InputError("invalid XML in {}: {}".format(label, error)) from error


def column_number(name):
    if not re.match(r"^[A-Za-z]+$", name):
        raise InputError("invalid Excel column name: {}".format(name))
    result = 0
    for character in name.upper():
        result = result * 26 + ord(character) - ord("A") + 1
    return result


def column_name(number):
    result = []
    while number > 0:
        number, remainder = divmod(number - 1, 26)
        result.append(chr(ord("A") + remainder))
    return "".join(reversed(result))


def cell_coordinates(reference):
    matched = CELL_REFERENCE_PATTERN.match(reference)
    if not matched:
        raise InputError("invalid Excel cell reference: {}".format(reference))
    return column_number(matched.group(1)), int(matched.group(2))


def xlsx_entry(archive, name, label):
    try:
        return archive.read(name)
    except KeyError as error:
        raise InputError("Excel file is missing {} ({})".format(label, name)) from error


def shared_strings(archive):
    try:
        content = archive.read("xl/sharedStrings.xml")
    except KeyError:
        return []
    root = safe_xml(content, "shared strings")
    result = []
    for item in root.findall("{{{}}}si".format(MAIN_NS)):
        result.append("".join(node.text or "" for node in item.iter("{{{}}}t".format(MAIN_NS))))
    return result


def worksheet_path(archive, sheet_name):
    workbook = safe_xml(xlsx_entry(archive, "xl/workbook.xml", "workbook metadata"), "workbook metadata")
    relationships = safe_xml(
        xlsx_entry(archive, "xl/_rels/workbook.xml.rels", "workbook relationships"),
        "workbook relationships",
    )
    relation_targets = {}
    for relation in relationships.findall("{{{}}}Relationship".format(PACKAGE_REL_NS)):
        relation_targets[relation.get("Id")] = relation.get("Target")

    for sheet in workbook.findall(".//{{{}}}sheet".format(MAIN_NS)):
        if sheet.get("name") != sheet_name:
            continue
        relation_id = sheet.get("{{{}}}id".format(REL_NS))
        target = relation_targets.get(relation_id)
        if not target:
            raise InputError("worksheet {} has no relationship target".format(sheet_name))
        if target.startswith("/"):
            normalized = posixpath.normpath(target.lstrip("/"))
        else:
            normalized = posixpath.normpath(posixpath.join("xl", target))
        if normalized.startswith("../") or normalized == "..":
            raise InputError("worksheet {} has an unsafe relationship target".format(sheet_name))
        return normalized
    available = [sheet.get("name") for sheet in workbook.findall(".//{{{}}}sheet".format(MAIN_NS))]
    raise InputError("worksheet {!r} was not found; available sheets: {}".format(sheet_name, ", ".join(available)))


def decode_cell(cell, strings):
    cell_type = cell.get("t", "")
    value_node = cell.find("{{{}}}v".format(MAIN_NS))
    formula_node = cell.find("{{{}}}f".format(MAIN_NS))
    has_formula = formula_node is not None
    has_cached_value = value_node is not None

    if cell_type == "inlineStr":
        value = "".join(node.text or "" for node in cell.iter("{{{}}}t".format(MAIN_NS)))
        return value, has_formula, True
    if value_node is None:
        return "", has_formula, False
    raw = value_node.text or ""
    if cell_type == "s":
        try:
            return strings[int(raw)], has_formula, True
        except (ValueError, IndexError) as error:
            raise InputError("cell has an invalid shared string index: {}".format(raw)) from error
    if cell_type == "b":
        return ("true" if raw == "1" else "false"), has_formula, True
    return raw, has_formula, has_cached_value


def read_sheet_cells(archive, sheet_name):
    strings = shared_strings(archive)
    path = worksheet_path(archive, sheet_name)
    root = safe_xml(xlsx_entry(archive, path, "worksheet {}".format(sheet_name)), "worksheet {}".format(sheet_name))
    cells = {}
    maximum_column = 0
    for cell in root.findall(".//{{{}}}c".format(MAIN_NS)):
        reference = cell.get("r", "")
        column, row = cell_coordinates(reference)
        value, has_formula, has_cached_value = decode_cell(cell, strings)
        cells[(row, column)] = {
            "value": value,
            "formula": has_formula,
            "cached": has_cached_value,
            "reference": reference,
        }
        maximum_column = max(maximum_column, column)
    return cells, maximum_column


def expected_rule(value, source_cell):
    if value == "<PRESENT>":
        return {"mode": "present", "source_cell": source_cell}
    if value == "<EMPTY>":
        return {"mode": "exact", "value": "", "source_cell": source_cell}
    if value.startswith("<REGEX:") and value.endswith(">"):
        pattern = value[len("<REGEX:"):-1]
        try:
            re.compile(pattern)
        except re.error as error:
            raise InputError("{} contains an invalid regular expression: {}".format(source_cell, error)) from error
        return {"mode": "regex", "value": pattern, "source_cell": source_cell}
    for marker, kind in (("<SECRET:", "Secret"), ("<CONFIGMAP:", "ConfigMap")):
        if value.startswith(marker) and value.endswith(">"):
            reference = value[len(marker):-1]
            if reference.count("/") != 1:
                raise InputError("{} must use {}name/key> syntax".format(source_cell, marker))
            name, key = reference.split("/", 1)
            if not name or not key:
                raise InputError("{} contains an empty resource name or key".format(source_cell))
            return {
                "mode": "source",
                "kind": kind,
                "name": name,
                "key": key,
                "source_cell": source_cell,
            }
    return {"mode": "exact", "value": value, "source_cell": source_cell}


def read_baseline(arguments):
    validate_file(arguments.input, "input_file", MAX_XLSX_SIZE)
    try:
        archive = zipfile.ZipFile(os.path.abspath(arguments.input), "r")
    except (OSError, zipfile.BadZipFile) as error:
        raise InputError("input_file is not a valid .xlsx archive: {}".format(error)) from error

    try:
        entries = archive.infolist()
        if len(entries) > MAX_ARCHIVE_ENTRIES:
            raise InputError("input_file contains too many archive entries")
        if sum(entry.file_size for entry in entries) > MAX_UNCOMPRESSED_SIZE:
            raise InputError("input_file expands beyond {} MiB".format(MAX_UNCOMPRESSED_SIZE // 1024 // 1024))
        cells, maximum_column = read_sheet_cells(archive, arguments.sheet)
    finally:
        archive.close()
    attribute_column = column_number(arguments.attribute_column)
    env_name_column = column_number(arguments.env_name_column)
    service_start_column = column_number(arguments.service_start_column)
    if service_start_column > maximum_column:
        raise InputError("service_start_column is beyond the used worksheet columns")

    components = {}
    component_columns = {}
    for column in range(service_start_column, maximum_column + 1):
        header = cells.get((arguments.header_row, column), {}).get("value", "").strip()
        if not header:
            continue
        if header in components:
            raise InputError("duplicate service column header: {}".format(header))
        components[header] = {}
        component_columns[column] = header
    if not components:
        raise InputError("no service columns were found in worksheet {}".format(arguments.sheet))

    formula_cells = []
    attribute_rows = 0
    environment_rows = 0
    skipped_unnamed_rows = 0
    maximum_row = max((row for row, _ in cells), default=arguments.header_row)
    for row in range(arguments.header_row + 1, maximum_row + 1):
        attribute = cells.get((row, attribute_column), {}).get("value", "").strip()
        if not attribute.startswith(arguments.attribute_prefix):
            continue
        attribute_rows += 1

        env_cell = cells.get((row, env_name_column))
        env_name = "" if env_cell is None else env_cell["value"].strip()
        if not env_name:
            skipped_unnamed_rows += 1
            continue
        environment_rows += 1
        if not ENV_NAME_PATTERN.match(env_name):
            raise InputError("{}{} contains invalid Kubernetes ENV name {!r}".format(arguments.env_name_column, row, env_name))

        populated = []
        for column, component in component_columns.items():
            cell = cells.get((row, column))
            if cell is None:
                continue
            if cell["formula"]:
                formula_cells.append(cell["reference"])
                if not cell["cached"]:
                    raise InputError("{} is a formula without a cached value; recalculate and save the workbook".format(cell["reference"]))
            if cell["value"] != "":
                populated.append((column, component, cell))
        if not populated:
            continue

        for _, component, cell in populated:
            if env_name in components[component]:
                previous = components[component][env_name]["source_cell"]
                raise InputError(
                    "duplicate ENV {} for service {} at {} and {}".format(
                        env_name, component, previous, cell["reference"]
                    )
                )
            components[component][env_name] = expected_rule(cell["value"], cell["reference"])

    expected_count = sum(len(values) for values in components.values())
    if attribute_rows == 0:
        raise InputError("no attributes beginning with {!r} were found".format(arguments.attribute_prefix))
    if environment_rows == 0:
        raise InputError(
            "no rows beginning with {!r} contain a Kubernetes ENV name in column {}".format(
                arguments.attribute_prefix, arguments.env_name_column
            )
        )
    if expected_count == 0:
        raise InputError("named Kubernetes ENV rows contain no service environment values")
    return {
        "components": components,
        "formula_cells": sorted(set(formula_cells)),
        "attribute_rows": attribute_rows,
        "environment_rows": environment_rows,
        "skipped_unnamed_rows": skipped_unnamed_rows,
        "expected_count": expected_count,
    }


def read_mapping(path):
    if not path:
        return {}
    content = read_file(path, "mapping_file", maximum=5 * 1024 * 1024)
    try:
        document = json.loads(content.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise InputError("cannot parse mapping_file: {}".format(error)) from error
    if not isinstance(document, dict) or document.get("apiVersion") != "syssetup/k8s-env-map/v1":
        raise InputError("mapping_file must use apiVersion syssetup/k8s-env-map/v1")
    components = document.get("components")
    if not isinstance(components, dict):
        raise InputError("mapping_file components must be an object")
    result = {}
    for component, mapping in components.items():
        if not isinstance(component, str) or not component or not isinstance(mapping, dict):
            raise InputError("mapping_file contains an invalid component mapping")
        unknown = set(mapping) - {"kind", "name", "container"}
        if unknown:
            raise InputError("mapping for {} contains unknown fields: {}".format(component, ", ".join(sorted(unknown))))
        if not mapping.get("name"):
            raise InputError("mapping for {} requires workload name".format(component))
        result[component] = mapping
    return result


def kubectl_json(arguments):
    command = ["kubectl"] + arguments + ["-o", "json"]
    try:
        result = subprocess.run(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
            timeout=COMMAND_TIMEOUT,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise KubernetesError("cannot execute {}: {}".format(" ".join(command), error)) from error
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip() or "kubectl returned exit code {}".format(result.returncode)
        raise KubernetesError("{}: {}".format(" ".join(command[:-2]), detail))
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise KubernetesError("kubectl returned invalid JSON: {}".format(error)) from error


def workload_snapshot(namespace):
    document = kubectl_json([
        "get",
        "deployment,statefulset,daemonset",
        "-n",
        namespace,
    ])
    items = document.get("items")
    if not isinstance(items, list):
        raise KubernetesError("kubectl workload response does not contain an items array")
    return items


def normalized_component(component):
    return component.strip().lower().replace("_", "-")


def resolve_workload(component, workloads, mappings):
    configured = mappings.get(component)
    normalized = normalized_component(component)
    if configured:
        candidates = [
            item for item in workloads
            if item.get("metadata", {}).get("name") == configured["name"]
            and (not configured.get("kind") or item.get("kind", "").lower() == configured["kind"].lower())
        ]
    else:
        candidates = [item for item in workloads if item.get("metadata", {}).get("name") == normalized]
        if not candidates:
            label_keys = ("app", "app.kubernetes.io/name", "component", "app.kubernetes.io/component")
            candidates = [
                item for item in workloads
                if any(item.get("metadata", {}).get("labels", {}).get(key) == normalized for key in label_keys)
            ]
        if not candidates:
            candidates = [
                item for item in workloads
                if item.get("metadata", {}).get("name", "").startswith(normalized + "-")
            ]
    if not candidates:
        raise KubernetesError("service column [{}] has no matching Kubernetes workload".format(component))
    if len(candidates) > 1:
        labels = ["{}/{}".format(item.get("kind", "?"), item.get("metadata", {}).get("name", "?")) for item in candidates]
        raise KubernetesError(
            "service column [{}] matches multiple workloads: {}; use mapping_file".format(component, ", ".join(labels))
        )
    workload = candidates[0]
    containers = workload.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    if not isinstance(containers, list) or not containers:
        raise KubernetesError("workload {}/{} has no containers".format(workload.get("kind"), workload.get("metadata", {}).get("name")))

    requested_container = configured.get("container") if configured else None
    if requested_container:
        matching = [container for container in containers if container.get("name") == requested_container]
    else:
        matching = [container for container in containers if container.get("name") == normalized]
        if not matching and len(containers) == 1:
            matching = containers
    if not matching:
        names = [container.get("name", "?") for container in containers]
        raise KubernetesError(
            "service column [{}] cannot select a container from {}/{}: {}; use mapping_file".format(
                component,
                workload.get("kind"),
                workload.get("metadata", {}).get("name"),
                ", ".join(names),
            )
        )
    return workload, matching[0]


class ResourceResolver:
    def __init__(self, namespace):
        self.namespace = namespace
        self.cache = {}
        self.issues = []

    def resource_values(self, kind, name, optional):
        cache_key = (kind, name)
        if cache_key in self.cache:
            return self.cache[cache_key]
        resource_name = "configmap" if kind == "ConfigMap" else "secret"
        try:
            document = kubectl_json(["get", resource_name, name, "-n", self.namespace])
        except KubernetesError as error:
            if optional:
                self.cache[cache_key] = {}
                return {}
            self.issues.append(str(error))
            self.cache[cache_key] = None
            return None

        result = {}
        if kind == "ConfigMap":
            for key, value in document.get("data", {}).items():
                result[key] = {"value": str(value), "sensitive": False}
        else:
            for key, value in document.get("data", {}).items():
                try:
                    decoded = base64.b64decode(value, validate=True).decode("utf-8")
                    result[key] = {"value": decoded, "sensitive": True}
                except (ValueError, UnicodeError) as error:
                    self.issues.append("Secret {}/{} cannot be decoded as UTF-8: {}".format(name, key, error))
        self.cache[cache_key] = result
        return result


def actual_value(value, kind="Literal", name="", key="", sensitive=False, unresolved=""):
    return {
        "value": value,
        "kind": kind,
        "name": name,
        "key": key,
        "sensitive": sensitive,
        "unresolved": unresolved,
    }


def resolve_reference(reference, resolver, optional):
    if "configMapKeyRef" in reference:
        item = reference["configMapKeyRef"]
        kind = "ConfigMap"
    elif "secretKeyRef" in reference:
        item = reference["secretKeyRef"]
        kind = "Secret"
    elif "fieldRef" in reference:
        return actual_value(None, kind="FieldRef", unresolved="fieldRef values are runtime-dependent")
    elif "resourceFieldRef" in reference:
        return actual_value(None, kind="ResourceFieldRef", unresolved="resourceFieldRef values are runtime-dependent")
    else:
        return actual_value(None, unresolved="unsupported valueFrom source")

    name = item.get("name", "")
    key = item.get("key", "")
    is_optional = item.get("optional", optional)
    values = resolver.resource_values(kind, name, is_optional)
    if values is None:
        return actual_value(None, kind=kind, name=name, key=key, unresolved="referenced resource could not be read")
    if key not in values:
        if is_optional:
            return None
        return actual_value(None, kind=kind, name=name, key=key, unresolved="referenced key does not exist")
    item_value = values[key]
    return actual_value(item_value["value"], kind=kind, name=name, key=key, sensitive=item_value["sensitive"])


def resolve_container_environment(container, resolver):
    result = {}
    for source in container.get("envFrom", []) or []:
        prefix = source.get("prefix", "")
        if "configMapRef" in source:
            reference = source["configMapRef"]
            kind = "ConfigMap"
        elif "secretRef" in source:
            reference = source["secretRef"]
            kind = "Secret"
        else:
            resolver.issues.append("container envFrom contains an unsupported source")
            continue
        name = reference.get("name", "")
        values = resolver.resource_values(kind, name, reference.get("optional", False))
        if values is None:
            continue
        for key, item in values.items():
            env_name = prefix + key
            if not ENV_NAME_PATTERN.match(env_name):
                continue
            result[env_name] = actual_value(
                item["value"], kind=kind, name=name, key=key, sensitive=item["sensitive"]
            )

    for environment in container.get("env", []) or []:
        name = environment.get("name", "")
        if not name:
            continue
        if "value" in environment:
            result[name] = actual_value(str(environment.get("value", "")))
            continue
        reference = resolve_reference(environment.get("valueFrom", {}), resolver, False)
        if reference is None:
            result.pop(name, None)
        else:
            result[name] = reference
    return result


def matches_expected(rule, actual, compare_values):
    if actual.get("unresolved"):
        return "unresolved"
    if rule["mode"] == "present":
        return "pass"
    if rule["mode"] == "source":
        matches = (
            actual.get("kind") == rule["kind"]
            and actual.get("name") == rule["name"]
            and actual.get("key") == rule["key"]
        )
        return "pass" if matches else "mismatch"
    if not compare_values:
        return "pass"
    if rule["mode"] == "exact":
        return "pass" if actual.get("value") == rule.get("value") else "mismatch"
    if rule["mode"] == "regex":
        value = actual.get("value")
        return "pass" if value is not None and re.fullmatch(rule["value"], value) else "mismatch"
    return "mismatch"


def display_text(value):
    text = "" if value is None else str(value)
    if len(text) > MAX_DISPLAY_LENGTH:
        text = text[:MAX_DISPLAY_LENGTH] + "..."
    return json.dumps(text, ensure_ascii=False)


def display_expected(rule):
    if rule["mode"] == "present":
        return "<PRESENT>"
    if rule["mode"] == "regex":
        return "<REGEX:{}>".format(rule["value"])
    if rule["mode"] == "source":
        return "<{}:{}/{}>".format(rule["kind"].upper(), rule["name"], rule["key"])
    return display_text(rule.get("value"))


def display_actual(actual):
    kind = actual.get("kind", "Literal")
    if kind == "Secret" or actual.get("sensitive"):
        return "<SECRET:{}/{}>".format(actual.get("name", "?"), actual.get("key", "?"))
    value = display_text(actual.get("value"))
    if kind == "ConfigMap":
        return "{} (<CONFIGMAP:{}/{}>)".format(value, actual.get("name", "?"), actual.get("key", "?"))
    return value


def ignored_extra(name, patterns):
    return any(fnmatch.fnmatchcase(name, pattern) for pattern in patterns)


def audit(arguments, baseline, mappings):
    workloads = workload_snapshot(arguments.namespace)
    resolver = ResourceResolver(arguments.namespace)
    ignore_patterns = [part for part in re.split(r"[\s,]+", arguments.ignore_extra.strip()) if part]
    stats = {
        "components": len(baseline["components"]),
        "expected": baseline["expected_count"],
        "passed": 0,
        "missing": 0,
        "mismatch": 0,
        "unresolved": 0,
        "extra": 0,
        "workload_errors": 0,
    }

    for component, expected in baseline["components"].items():
        if not expected:
            continue
        print("\nComponent [{}]".format(component))
        try:
            workload, container = resolve_workload(component, workloads, mappings)
        except KubernetesError as error:
            stats["workload_errors"] += 1
            print("[FAIL] [WORKLOAD] {}".format(error))
            continue

        workload_label = "{}/{} container={}".format(
            workload.get("kind", "?"),
            workload.get("metadata", {}).get("name", "?"),
            container.get("name", "?"),
        )
        print("  {}".format(workload_label))
        issue_start = len(resolver.issues)
        actual = resolve_container_environment(container, resolver)
        for issue in resolver.issues[issue_start:]:
            stats["unresolved"] += 1
            print("[FAIL] [UNRESOLVED] {}: {}".format(component, issue))

        for env_name, rule in sorted(expected.items()):
            if env_name not in actual:
                stats["missing"] += 1
                print("[FAIL] [MISSING] {}/{} expected by {}".format(component, env_name, rule["source_cell"]))
                continue
            status = matches_expected(rule, actual[env_name], arguments.compare_values)
            if status == "pass":
                stats["passed"] += 1
                if arguments.show_pass:
                    print("[PASS] [MATCH] {}/{} source={}".format(component, env_name, rule["source_cell"]))
            elif status == "unresolved":
                stats["unresolved"] += 1
                print(
                    "[FAIL] [UNRESOLVED] {}/{}: {} (expected by {})".format(
                        component, env_name, actual[env_name]["unresolved"], rule["source_cell"]
                    )
                )
            else:
                stats["mismatch"] += 1
                print(
                    "[FAIL] [MISMATCH] {}/{} expected={} actual={} source={}".format(
                        component,
                        env_name,
                        display_expected(rule),
                        display_actual(actual[env_name]),
                        rule["source_cell"],
                    )
                )

        for env_name in sorted(set(actual) - set(expected)):
            if ignored_extra(env_name, ignore_patterns):
                continue
            stats["extra"] += 1
            if arguments.extra_policy == "fail":
                print("[FAIL] [EXTRA] {}/{} is not defined in Excel".format(component, env_name))
            elif arguments.extra_policy == "warn":
                print("[WARN] [EXTRA] {}/{} is not defined in Excel".format(component, env_name))

    failures = stats["missing"] + stats["mismatch"] + stats["unresolved"] + stats["workload_errors"]
    if arguments.extra_policy == "fail":
        failures += stats["extra"]
    stats["failures"] = failures
    return stats


def boolean_argument(value):
    if value == "true":
        return True
    if value == "false":
        return False
    raise argparse.ArgumentTypeError("expected true or false")


def parse_arguments(values):
    parser = argparse.ArgumentParser(description="Compare Kubernetes environments with an Excel baseline")
    parser.add_argument("--input", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--sheet", default="VDU")
    parser.add_argument("--header-row", type=int, default=1)
    parser.add_argument("--attribute-column", default="B")
    parser.add_argument("--env-name-column", default="C")
    parser.add_argument("--service-start-column", default="D")
    parser.add_argument("--attribute-prefix", default="environments_")
    parser.add_argument("--mapping-file", default="")
    parser.add_argument("--compare-values", type=boolean_argument, default=True)
    parser.add_argument("--extra-policy", choices=("fail", "warn", "ignore"), default="warn")
    parser.add_argument("--ignore-extra", default="")
    parser.add_argument("--show-pass", type=boolean_argument, default=False)
    arguments = parser.parse_args(values)
    if arguments.header_row < 1:
        parser.error("--header-row must be positive")
    for name in ("attribute_column", "env_name_column", "service_start_column"):
        setattr(arguments, name, getattr(arguments, name).upper())
    return arguments


def main(values):
    arguments = parse_arguments(values)
    baseline = read_baseline(arguments)
    mappings = read_mapping(arguments.mapping_file)
    print("Kubernetes environment audit")
    print("  namespace={}".format(arguments.namespace))
    print("  workbook={} sheet={}".format(arguments.input, arguments.sheet))
    print(
        "  components={} attribute_rows={} selected_rows={} skipped_unnamed={} expected={} formula_cells={}".format(
            len(baseline["components"]),
            baseline["attribute_rows"],
            baseline["environment_rows"],
            baseline["skipped_unnamed_rows"],
            baseline["expected_count"],
            len(baseline["formula_cells"]),
        )
    )
    if baseline["formula_cells"]:
        print(
            "[WARN] Using cached values from {} Excel formula cell(s); recalculate and save the workbook before auditing.".format(
                len(baseline["formula_cells"])
            )
        )
    stats = audit(arguments, baseline, mappings)
    status = "PASS" if stats["failures"] == 0 else "FAIL"
    print(
        "\n[{}] Summary: components={} expected={} passed={} missing={} mismatch={} "
        "unresolved={} extra={} workload_errors={} failures={}".format(
            status,
            stats["components"],
            stats["expected"],
            stats["passed"],
            stats["missing"],
            stats["mismatch"],
            stats["unresolved"],
            stats["extra"],
            stats["workload_errors"],
            stats["failures"],
        )
    )
    return 0 if stats["failures"] == 0 else 1


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except InputError as error:
        print("Input error: {}".format(error), file=sys.stderr)
        sys.exit(2)
    except KubernetesError as error:
        print("Kubernetes error: {}".format(error), file=sys.stderr)
        sys.exit(2)
