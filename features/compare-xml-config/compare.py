#!/usr/bin/env python3
import json
import os
import re
import sys
import xml.etree.ElementTree as ET


MAX_INPUT_SIZE = 50 * 1024 * 1024
KEY_NAMES = ("id", "name", "no", "key")


class InputError(Exception):
    pass


def local_name(tag):
    return tag.rsplit("}", 1)[-1]


def read_bytes(path, label):
    try:
        if not os.path.isfile(path):
            raise InputError("{} is not a regular file: {}".format(label, path))
        size = os.path.getsize(path)
        if size > MAX_INPUT_SIZE:
            raise InputError("{} is larger than 50 MiB".format(label))
        with open(path, "rb") as handle:
            return handle.read()
    except OSError as error:
        raise InputError("cannot read {}: {}".format(label, error)) from error


def parse_xml(path, label):
    content = read_bytes(path, label)
    upper = content.upper()
    if b"<!DOCTYPE" in upper or b"<!ENTITY" in upper:
        raise InputError("{} contains a DTD or entity declaration, which is not allowed".format(label))
    try:
        return ET.fromstring(content)
    except ET.ParseError as error:
        raise InputError("invalid {}: {}".format(label, error)) from error


def read_rules(path):
    content = read_bytes(path, "rules file")
    try:
        rules = json.loads(content.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise InputError("cannot parse rules file: {}".format(error)) from error
    if not isinstance(rules, dict) or rules.get("apiVersion") != "syssetup/xml-check/v1":
        raise InputError("rules file must use apiVersion syssetup/xml-check/v1")
    checks = rules.get("checks")
    if not isinstance(checks, list) or not checks:
        raise InputError("rules.checks must be a non-empty array")

    result = []
    seen = set()
    for index, check in enumerate(checks, start=1):
        if not isinstance(check, dict):
            raise InputError("critical check {} must be an object".format(index))
        check_id = str(check.get("id", "")).strip()
        xpath = str(check.get("xpath", "")).strip()
        if not check_id or not xpath:
            raise InputError("critical check {} requires id and xpath".format(index))
        if check_id in seen:
            raise InputError("duplicate critical check id: {}".format(check_id))
        seen.add(check_id)
        required = check.get("required", True)
        sensitive = check.get("sensitive", False)
        if not isinstance(required, bool) or not isinstance(sensitive, bool):
            raise InputError("critical check {}: required and sensitive must be boolean".format(check_id))
        prefix = xpath_prefix(xpath)
        result.append({
            "id": check_id,
            "xpath": xpath,
            "prefix": prefix,
            "required": required,
            "sensitive": sensitive,
        })
    return result


def xpath_prefix(xpath):
    names = [match[1] for match in re.findall(r"local-name\(\)\s*=\s*(['\"])(.*?)\1", xpath)]
    if not names:
        simple = xpath.strip().strip("/")
        if not simple or any(character in simple for character in "[]()*|"):
            raise InputError("critical XPath is not a supported element path: {}".format(xpath))
        names = [part.split(":", 1)[-1] for part in simple.split("/") if part]
    return "/" + "/".join(names)


def direct_leaf_value(element, name):
    matches = []
    for child in element:
        if not isinstance(child.tag, str) or local_name(child.tag) != name:
            continue
        if any(isinstance(grandchild.tag, str) for grandchild in child):
            continue
        matches.append((child.text or "").strip())
    if len(matches) != 1 or matches[0] == "":
        return None
    return matches[0]


def stable_keys(elements):
    for key_name in KEY_NAMES:
        values = [direct_leaf_value(element, key_name) for element in elements]
        if all(value is not None for value in values) and len(set(values)) == len(values):
            return key_name, values
    return None, None


def child_segments(children):
    groups = {}
    for child in children:
        groups.setdefault(local_name(child.tag), []).append(child)

    segments = {}
    for name, elements in groups.items():
        if len(elements) == 1:
            segments[id(elements[0])] = name
            continue
        key_name, values = stable_keys(elements)
        for index, element in enumerate(elements):
            if key_name is None:
                segment = "{}[{}]".format(name, index + 1)
            else:
                key_value = json.dumps(values[index], ensure_ascii=False)
                segment = "{}[{}={}]".format(name, key_name, key_value)
            segments[id(element)] = segment
    return segments


def flatten(element):
    paths = {}

    def walk(current, path):
        for attribute, value in sorted(current.attrib.items(), key=lambda item: local_name(item[0])):
            paths[path + "/@" + local_name(attribute)] = value.strip()

        children = [child for child in current if isinstance(child.tag, str)]
        if not children:
            paths[path] = (current.text or "").strip()
            return
        segments = child_segments(children)
        for child in children:
            walk(child, path + "/" + segments[id(child)])

    walk(element, "/" + local_name(element.tag))
    return paths


def path_under(path, prefix):
    structural = "/" + "/".join(
        segment.split("[", 1)[0] for segment in path.strip("/").split("/")
    )
    return structural == prefix or structural.startswith(prefix + "/")


def critical_for(path, rules):
    return [rule for rule in rules if path_under(path, rule["prefix"])]


def display(value, sensitive):
    if value is None:
        return "<missing>"
    if sensitive:
        return "<redacted>"
    return repr(value)


def main(arguments):
    if len(arguments) != 5:
        raise InputError("usage: compare.py SOURCE_XML TARGET_XML RULES_FILE SHOW_EQUAL")
    source_path, target_path, rules_path, show_equal_text = arguments[1:]
    if show_equal_text not in {"true", "false"}:
        raise InputError("SHOW_EQUAL must be true or false")
    show_equal = show_equal_text == "true"

    source = flatten(parse_xml(source_path, "source XML"))
    target = flatten(parse_xml(target_path, "target XML"))
    rules = read_rules(rules_path)
    paths = sorted(set(source) | set(target))

    same_paths = []
    changed_paths = []
    only_source = []
    only_target = []
    critical_paths = {}
    for path in paths:
        matched_rules = critical_for(path, rules)
        if matched_rules:
            critical_paths[path] = matched_rules
        if path not in target:
            only_source.append(path)
        elif path not in source:
            only_target.append(path)
        elif source[path] == target[path]:
            same_paths.append(path)
        else:
            changed_paths.append(path)

    different_count = len(changed_paths) + len(only_source) + len(only_target)
    critical_same = sum(1 for path in same_paths if path in critical_paths)
    critical_different = sum(
        1 for path in changed_paths + only_source + only_target if path in critical_paths
    )
    matched_rule_ids = {
        rule["id"] for matched_rules in critical_paths.values() for rule in matched_rules
    }
    missing_rules = [rule for rule in rules if rule["id"] not in matched_rule_ids]
    missing_required = sum(1 for rule in missing_rules if rule["required"])

    summary_status = "PASS" if different_count == 0 and missing_required == 0 else "FAIL"
    print("Comparing XML files")
    print("  source={}".format(source_path))
    print("  target={}".format(target_path))
    print(
        "[{}] Summary: total_paths={} same={} different={} changed={} "
        "only_source={} only_target={}".format(
            summary_status,
            len(paths),
            len(same_paths),
            different_count,
            len(changed_paths),
            len(only_source),
            len(only_target),
        )
    )
    print(
        "[{}] Critical summary: rules={} matched_rules={} total_paths={} same={} "
        "different={} missing_required_rules={}".format(
            summary_status,
            len(rules),
            len(matched_rule_ids),
            len(critical_paths),
            critical_same,
            critical_different,
            missing_required,
        )
    )

    print("Details:")
    for rule in missing_rules:
        status = "FAIL" if rule["required"] else "SKIP"
        print("[{}] [CRITICAL:{}] [MISSING] {}".format(status, rule["id"], rule["xpath"]))

    for path in paths:
        matched_rules = critical_paths.get(path, [])
        is_critical = bool(matched_rules)
        if path in same_paths and not is_critical and not show_equal:
            continue
        critical_label = ""
        if is_critical:
            critical_label = " [CRITICAL:{}]".format(",".join(rule["id"] for rule in matched_rules))
        sensitive = any(rule["sensitive"] for rule in matched_rules)
        if path in same_paths:
            print("[PASS]{} [SAME] {}".format(critical_label, path))
            print("  value={}".format(display(source[path], sensitive)))
        elif path in changed_paths:
            print("[FAIL]{} [CHANGED] {}".format(critical_label, path))
            print("  source={}".format(display(source[path], sensitive)))
            print("  target={}".format(display(target[path], sensitive)))
        elif path in only_source:
            print("[FAIL]{} [ONLY_SOURCE] {}".format(critical_label, path))
            print("  source={}".format(display(source[path], sensitive)))
            print("  target=<missing>")
        else:
            print("[FAIL]{} [ONLY_TARGET] {}".format(critical_label, path))
            print("  source=<missing>")
            print("  target={}".format(display(target[path], sensitive)))

    return 0 if summary_status == "PASS" else 1


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv))
    except InputError as error:
        print("Error: {}".format(error), file=sys.stderr)
        sys.exit(2)
