#!/usr/bin/env python3
import argparse
import fnmatch
import json
import os
import posixpath
import re
import subprocess
import sys
import zipfile
from decimal import Decimal, InvalidOperation
import xml.etree.ElementTree as ET


MAX_XLSX_SIZE = 50 * 1024 * 1024
MAX_UNCOMPRESSED_SIZE = 250 * 1024 * 1024
MAX_ARCHIVE_ENTRIES = 10000
COMMAND_TIMEOUT = 60
CELL_REFERENCE_PATTERN = re.compile(r"^([A-Za-z]+)([0-9]+)$")
QUANTITY_PATTERN = re.compile(
    r"^([+-]?(?:(?:[0-9]+(?:\.[0-9]*)?)|(?:\.[0-9]+))(?:[eE][+-]?[0-9]+)?)([A-Za-z]*)$"
)
MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
PACKAGE_REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"

RESOURCE_ATTRIBUTES = {
    "mem_size": ("container", "requests", "memory", "memory"),
    "num_cpus": ("container", "requests", "cpu", "cpu"),
    "mem_size_limit": ("container", "limits", "memory", "memory"),
    "num_cpus_limit": ("container", "limits", "cpu", "cpu"),
    "init_resources_cpu": ("initContainer", "requests", "cpu", "cpu"),
    "init_resources_mem": ("initContainer", "requests", "memory", "memory"),
    "pv_storage": ("persistentVolume", "capacity", "storage", "memory"),
}
REQUIRED_RESOURCE_ATTRIBUTES = set(RESOURCE_ATTRIBUTES) - {"pv_storage"}
PV_STORAGE_PATH = "persistentVolume.capacity.storage"

CPU_MULTIPLIERS = {
    "": Decimal(1),
    "m": Decimal("0.001"),
    "u": Decimal("0.000001"),
    "n": Decimal("0.000000001"),
}
MEMORY_MULTIPLIERS = {
    "": Decimal(1),
    "n": Decimal("0.000000001"),
    "u": Decimal("0.000001"),
    "m": Decimal("0.001"),
    "k": Decimal(1000),
    "K": Decimal(1000),
    "M": Decimal(1000) ** 2,
    "G": Decimal(1000) ** 3,
    "T": Decimal(1000) ** 4,
    "P": Decimal(1000) ** 5,
    "E": Decimal(1000) ** 6,
    "Ki": Decimal(1024),
    "Mi": Decimal(1024) ** 2,
    "Gi": Decimal(1024) ** 3,
    "Ti": Decimal(1024) ** 4,
    "Pi": Decimal(1024) ** 5,
    "Ei": Decimal(1024) ** 6,
}
QUOTE_PAIRS = {"\"": "\"", "'": "'", "\u201c": "\u201d", "\u2018": "\u2019"}


class InputError(Exception):
    pass


class KubernetesError(Exception):
    pass


class QuantityError(ValueError):
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


def clean_quantity(value):
    result = str(value).strip()
    while len(result) >= 2 and result[0] in QUOTE_PAIRS and result[-1] == QUOTE_PAIRS[result[0]]:
        result = result[1:-1].strip()
    return result


def parse_quantity(value, resource_type, default_unit="", apply_default=False):
    cleaned = clean_quantity(value)
    matched = QUANTITY_PATTERN.fullmatch(cleaned)
    if not matched:
        raise QuantityError("invalid Kubernetes quantity {!r}".format(cleaned))
    number_text, suffix = matched.groups()
    if apply_default and suffix == "":
        suffix = default_unit
    multipliers = CPU_MULTIPLIERS if resource_type == "cpu" else MEMORY_MULTIPLIERS
    if suffix not in multipliers:
        raise QuantityError("unit {!r} is not valid for {}".format(suffix, resource_type))
    try:
        number = Decimal(number_text)
    except InvalidOperation as error:
        raise QuantityError("invalid numeric quantity {!r}".format(number_text)) from error
    if not number.is_finite() or number < 0:
        raise QuantityError("quantity must be a finite non-negative number")
    return number * multipliers[suffix]


def resource_path(scope, group, resource_name):
    return "{}.{}.{}".format(scope, group, resource_name)


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
            raise InputError("duplicate component column header: {}".format(header))
        components[header] = {}
        component_columns[column] = header
    if not components:
        raise InputError("no component columns were found in worksheet {}".format(arguments.sheet))

    found_attributes = {}
    formula_cells = []
    maximum_row = max((row for row, _ in cells), default=arguments.header_row)
    for row in range(arguments.header_row + 1, maximum_row + 1):
        attribute = cells.get((row, attribute_column), {}).get("value", "").strip()
        if attribute not in RESOURCE_ATTRIBUTES:
            continue
        if attribute in found_attributes:
            raise InputError(
                "duplicate resource attribute {} at rows {} and {}".format(attribute, found_attributes[attribute], row)
            )
        found_attributes[attribute] = row
        scope, group, resource_name, resource_type = RESOURCE_ATTRIBUTES[attribute]
        default_unit = arguments.cpu_default_unit if resource_type == "cpu" else arguments.memory_default_unit
        for column, component in component_columns.items():
            cell = cells.get((row, column))
            if cell is None:
                continue
            if cell["formula"]:
                formula_cells.append(cell["reference"])
                if not cell["cached"]:
                    raise InputError(
                        "{} is a formula without a cached value; recalculate and save the workbook".format(cell["reference"])
                    )
            raw = clean_quantity(cell["value"])
            if raw == "":
                continue
            try:
                normalized = parse_quantity(raw, resource_type, default_unit, apply_default=True)
            except QuantityError as error:
                raise InputError("{} contains {}".format(cell["reference"], error)) from error
            path = resource_path(scope, group, resource_name)
            if path in components[component]:
                raise InputError("duplicate resource {} for component {}".format(path, component))
            matched_quantity = QUANTITY_PATTERN.fullmatch(raw)
            display = raw if matched_quantity.group(2) else raw + default_unit
            components[component][path] = {
                "normalized": normalized,
                "display": display,
                "resource_type": resource_type,
                "source_cell": cell["reference"],
            }

    missing_attributes = sorted(REQUIRED_RESOURCE_ATTRIBUTES - set(found_attributes))
    if missing_attributes:
        raise InputError("worksheet is missing resource attributes: {}".format(", ".join(missing_attributes)))
    expected_count = sum(len(resources) for resources in components.values())
    if expected_count == 0:
        raise InputError("the Excel baseline contains no component resource values")
    return {
        "components": components,
        "resource_rows": len(found_attributes),
        "expected_count": expected_count,
        "formula_cells": sorted(set(formula_cells)),
    }


def read_mapping(path):
    if not path:
        return {}
    content = read_file(path, "mapping_file", maximum=5 * 1024 * 1024)
    try:
        document = json.loads(content.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise InputError("cannot parse mapping_file: {}".format(error)) from error
    if not isinstance(document, dict) or document.get("apiVersion") != "syssetup/k8s-resource-map/v1":
        raise InputError("mapping_file must use apiVersion syssetup/k8s-resource-map/v1")
    components = document.get("components")
    if not isinstance(components, dict):
        raise InputError("mapping_file components must be an object")
    result = {}
    for component, mapping in components.items():
        if not isinstance(component, str) or not component or not isinstance(mapping, dict):
            raise InputError("mapping_file contains an invalid component mapping")
        unknown = set(mapping) - {"kind", "name", "container", "initContainer"}
        if unknown:
            raise InputError("mapping for {} contains unknown fields: {}".format(component, ", ".join(sorted(unknown))))
        if not isinstance(mapping.get("name"), str) or not mapping["name"]:
            raise InputError("mapping for {} requires workload name".format(component))
        for field in ("kind", "container", "initContainer"):
            if field in mapping and (not isinstance(mapping[field], str) or not mapping[field]):
                raise InputError("mapping field {} for {} must be a non-empty string".format(field, component))
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
    document = kubectl_json(["get", "deployment,statefulset,daemonset", "-n", namespace])
    items = document.get("items")
    if not isinstance(items, list):
        raise KubernetesError("kubectl workload response does not contain an items array")
    return items


def storage_snapshot(namespace):
    claim_document = kubectl_json(["get", "persistentvolumeclaim", "-n", namespace])
    volume_document = kubectl_json(["get", "persistentvolume"])
    claims = claim_document.get("items")
    volumes = volume_document.get("items")
    if not isinstance(claims, list):
        raise KubernetesError("kubectl persistentvolumeclaim response does not contain an items array")
    if not isinstance(volumes, list):
        raise KubernetesError("kubectl persistentvolume response does not contain an items array")
    return claims, volumes


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
        raise KubernetesError("component [{}] has no matching Kubernetes workload".format(component))
    if len(candidates) > 1:
        labels = ["{}/{}".format(item.get("kind", "?"), item.get("metadata", {}).get("name", "?")) for item in candidates]
        raise KubernetesError("component [{}] matches multiple workloads: {}; use mapping_file".format(component, ", ".join(labels)))
    return candidates[0]


def select_container(component, workload, mappings, init=False):
    configured = mappings.get(component, {})
    field = "initContainer" if init else "container"
    collection = "initContainers" if init else "containers"
    role = "init container" if init else "container"
    containers = workload.get("spec", {}).get("template", {}).get("spec", {}).get(collection, []) or []
    if not isinstance(containers, list) or not containers:
        raise KubernetesError("workload {}/{} has no {}s".format(workload.get("kind"), workload.get("metadata", {}).get("name"), role))
    requested = configured.get(field)
    normalized = normalized_component(component)
    if requested:
        matching = [container for container in containers if container.get("name") == requested]
    else:
        preferred = (normalized + "-init", normalized) if init else (normalized,)
        matching = [container for container in containers if container.get("name") in preferred]
        if len(matching) != 1 and len(containers) == 1:
            matching = containers
    if len(matching) != 1:
        names = [container.get("name", "?") for container in containers]
        raise KubernetesError(
            "component [{}] cannot select {} from {}/{}: {}; use mapping_file".format(
                component, role, workload.get("kind"), workload.get("metadata", {}).get("name"), ", ".join(names)
            )
        )
    return matching[0]


def flatten_resources(container, scope):
    result = {}
    resources = container.get("resources", {}) or {}
    if not isinstance(resources, dict):
        return result
    for group in ("requests", "limits"):
        values = resources.get(group, {}) or {}
        if not isinstance(values, dict):
            continue
        for resource_name, value in values.items():
            result[resource_path(scope, group, resource_name)] = str(value)
    return result


def workload_persistent_volumes(workload, claims, volumes):
    workload_name = workload.get("metadata", {}).get("name", "")
    claim_names = set()
    pod_spec = workload.get("spec", {}).get("template", {}).get("spec", {}) or {}
    for volume in pod_spec.get("volumes", []) or []:
        claim_name = (volume.get("persistentVolumeClaim") or {}).get("claimName")
        if claim_name:
            claim_names.add(claim_name)

    if workload.get("kind", "").lower() == "statefulset":
        for template in workload.get("spec", {}).get("volumeClaimTemplates", []) or []:
            template_name = template.get("metadata", {}).get("name", "")
            prefix = "{}-{}-".format(template_name, workload_name)
            if template_name and workload_name:
                claim_names.update(
                    claim.get("metadata", {}).get("name")
                    for claim in claims
                    if claim.get("metadata", {}).get("name", "").startswith(prefix)
                )

    claims_by_name = {claim.get("metadata", {}).get("name"): claim for claim in claims}
    volumes_by_name = {volume.get("metadata", {}).get("name"): volume for volume in volumes}
    resolved = []
    for claim_name in sorted(name for name in claim_names if name):
        claim = claims_by_name.get(claim_name)
        if claim is None:
            resolved.append({"pvc": claim_name, "error": "PVC was not found"})
            continue
        volume_name = claim.get("spec", {}).get("volumeName", "")
        if not volume_name:
            resolved.append({"pvc": claim_name, "error": "PVC is not bound to a PV"})
            continue
        volume = volumes_by_name.get(volume_name)
        if volume is None:
            resolved.append({"pvc": claim_name, "pv": volume_name, "error": "PV was not found"})
            continue
        storage = (volume.get("spec", {}).get("capacity", {}) or {}).get("storage")
        if storage is None:
            resolved.append({"pvc": claim_name, "pv": volume_name, "error": "PV has no storage capacity"})
            continue
        resolved.append({"pvc": claim_name, "pv": volume_name, "storage": str(storage)})
    return resolved


def display_persistent_volumes(resolved):
    if not resolved:
        return "no PVC referenced by workload"
    details = []
    for item in resolved:
        if "error" in item:
            details.append("{} ({})".format(item.get("pvc", "?"), item["error"]))
        else:
            details.append("{} (pvc={}, pv={})".format(item["storage"], item["pvc"], item["pv"]))
    return ", ".join(details)


def ignored_extra(path, patterns):
    return any(fnmatch.fnmatchcase(path, pattern) for pattern in patterns)


def audit(arguments, baseline, mappings):
    workloads = workload_snapshot(arguments.namespace)
    checks_storage = any(PV_STORAGE_PATH in expected for expected in baseline["components"].values())
    claims, volumes = storage_snapshot(arguments.namespace) if checks_storage else ([], [])
    ignore_patterns = [part for part in re.split(r"[\s,]+", arguments.ignore_extra.strip()) if part]
    stats = {
        "components": len(baseline["components"]),
        "expected": baseline["expected_count"],
        "passed": 0,
        "missing": 0,
        "mismatch": 0,
        "extra": 0,
        "resolution_errors": 0,
    }

    for component, expected in baseline["components"].items():
        if not expected:
            continue
        print("\nComponent [{}]".format(component))
        try:
            workload = resolve_workload(component, workloads, mappings)
            needs_main = any(path.startswith("container.") for path in expected)
            needs_init = any(path.startswith("initContainer.") for path in expected)
            main_container = select_container(component, workload, mappings) if needs_main else None
            init_container = select_container(component, workload, mappings, init=True) if needs_init else None
        except KubernetesError as error:
            stats["resolution_errors"] += 1
            print("[FAIL] [RESOLUTION] {}".format(error))
            continue

        selected = []
        actual = {}
        if main_container is not None:
            selected.append("container={}".format(main_container.get("name", "?")))
            actual.update(flatten_resources(main_container, "container"))
        if init_container is not None:
            selected.append("initContainer={}".format(init_container.get("name", "?")))
            actual.update(flatten_resources(init_container, "initContainer"))
        print(
            "  {}/{} {}".format(
                workload.get("kind", "?"),
                workload.get("metadata", {}).get("name", "?"),
                " ".join(selected),
            )
        )

        for path, rule in sorted(expected.items()):
            if path == PV_STORAGE_PATH:
                resolved = workload_persistent_volumes(workload, claims, volumes)
                actual_display = display_persistent_volumes(resolved)
                if not resolved or any("error" in item for item in resolved):
                    stats["missing"] += 1
                    print(
                        "[FAIL] [MISSING] {}/{} expected={} actual={} source={}".format(
                            component, path, rule["display"], actual_display, rule["source_cell"]
                        )
                    )
                    continue
                try:
                    matches = all(
                        parse_quantity(item["storage"], rule["resource_type"]) == rule["normalized"]
                        for item in resolved
                    )
                except QuantityError as error:
                    stats["mismatch"] += 1
                    print(
                        "[FAIL] [MISMATCH] {}/{} expected={} actual={} error={} source={}".format(
                            component, path, rule["display"], actual_display, error, rule["source_cell"]
                        )
                    )
                    continue
                if matches:
                    stats["passed"] += 1
                    if arguments.show_pass:
                        print(
                            "[PASS] [MATCH] {}/{} expected={} actual={} source={}".format(
                                component, path, rule["display"], actual_display, rule["source_cell"]
                            )
                        )
                else:
                    stats["mismatch"] += 1
                    print(
                        "[FAIL] [MISMATCH] {}/{} expected={} actual={} source={}".format(
                            component, path, rule["display"], actual_display, rule["source_cell"]
                        )
                    )
                continue
            if path not in actual:
                stats["missing"] += 1
                print("[FAIL] [MISSING] {}/{} expected={} source={}".format(component, path, rule["display"], rule["source_cell"]))
                continue
            try:
                deployed = parse_quantity(actual[path], rule["resource_type"])
            except QuantityError as error:
                stats["mismatch"] += 1
                print("[FAIL] [MISMATCH] {}/{} has invalid deployed value: {} source={}".format(component, path, error, rule["source_cell"]))
                continue
            if deployed == rule["normalized"]:
                stats["passed"] += 1
                if arguments.show_pass:
                    print(
                        "[PASS] [MATCH] {}/{} expected={} actual={} source={}".format(
                            component,
                            path,
                            rule["display"],
                            clean_quantity(actual[path]),
                            rule["source_cell"],
                        )
                    )
            else:
                stats["mismatch"] += 1
                print(
                    "[FAIL] [MISMATCH] {}/{} expected={} actual={} source={}".format(
                        component, path, rule["display"], clean_quantity(actual[path]), rule["source_cell"]
                    )
                )

        for path in sorted(set(actual) - set(expected)):
            if ignored_extra(path, ignore_patterns):
                continue
            stats["extra"] += 1
            if arguments.extra_policy == "fail":
                print("[FAIL] [EXTRA] {}/{} is not defined in Excel".format(component, path))
            elif arguments.extra_policy == "warn":
                print("[WARN] [EXTRA] {}/{} is not defined in Excel".format(component, path))

    failures = stats["missing"] + stats["mismatch"] + stats["resolution_errors"]
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
    parser = argparse.ArgumentParser(description="Compare Kubernetes resources with an Excel baseline")
    parser.add_argument("--input", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--sheet", default="VDU")
    parser.add_argument("--header-row", type=int, default=1)
    parser.add_argument("--attribute-column", default="B")
    parser.add_argument("--service-start-column", default="D")
    parser.add_argument("--mapping-file", default="")
    parser.add_argument("--cpu-default-unit", default="m")
    parser.add_argument("--memory-default-unit", default="Mi")
    parser.add_argument("--extra-policy", choices=("fail", "warn", "ignore"), default="warn")
    parser.add_argument("--ignore-extra", default="")
    parser.add_argument("--show-pass", type=boolean_argument, default=False)
    arguments = parser.parse_args(values)
    if arguments.header_row < 1:
        parser.error("--header-row must be positive")
    for name in ("attribute_column", "service_start_column"):
        setattr(arguments, name, getattr(arguments, name).upper())
    try:
        parse_quantity("1" + arguments.cpu_default_unit, "cpu")
        parse_quantity("1" + arguments.memory_default_unit, "memory")
    except QuantityError as error:
        parser.error("invalid default unit: {}".format(error))
    return arguments


def main(values):
    arguments = parse_arguments(values)
    baseline = read_baseline(arguments)
    mappings = read_mapping(arguments.mapping_file)
    unknown_mappings = sorted(set(mappings) - set(baseline["components"]))
    if unknown_mappings:
        raise InputError("mapping_file contains components absent from Excel: {}".format(", ".join(unknown_mappings)))
    print("Kubernetes resource audit")
    print("  namespace={}".format(arguments.namespace))
    print("  workbook={} sheet={}".format(arguments.input, arguments.sheet))
    print(
        "  components={} resource_rows={} expected={} formula_cells={}".format(
            len(baseline["components"]),
            baseline["resource_rows"],
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
        "extra={} resolution_errors={} failures={}".format(
            status,
            stats["components"],
            stats["expected"],
            stats["passed"],
            stats["missing"],
            stats["mismatch"],
            stats["extra"],
            stats["resolution_errors"],
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
