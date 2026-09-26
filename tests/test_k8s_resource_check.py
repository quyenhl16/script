


import contextlib
from decimal import Decimal
import importlib.util
import io
import os
import tempfile
import types
import unittest
import zipfile


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CHECKER_PATH = os.path.join(ROOT, "features", "k8s-resource-check", "check.py")


def load_checker():
    spec = importlib.util.spec_from_file_location("k8s_resource_check", CHECKER_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def inline_cell(reference, value):
    escaped = (
        value.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )
    return '<c r="{}" t="inlineStr"><is><t>{}</t></is></c>'.format(reference, escaped)


def write_workbook(path, omit_attribute="", include_pv=False):
    attributes = [
        ("mem_size", "4096", "32768"),
        ("num_cpus", "4000", "20000"),
        ("mem_size_limit", "4096", "32768"),
        ("num_cpus_limit", "4000", "20000"),
        ("init_resources_cpu", "", "\u201c2000m\u201d"),
        ("init_resources_mem", "", "\u201c4Gi\u201d"),
    ]
    if include_pv:
        attributes.append(("pv_storage", "10240", ""))
    rows = [
        '<row r="1">{}</row>'.format("".join([
            inline_cell("B1", "Thuộc tính"),
            inline_cell("D1", "comm"),
            inline_cell("E1", "aerospike"),
        ]))
    ]
    for row_number, (attribute, comm_value, aerospike_value) in enumerate(attributes, 2):
        if attribute == omit_attribute:
            continue
        cells = [inline_cell("B{}".format(row_number), attribute)]
        if comm_value:
            if attribute == "num_cpus_limit":
                cells.append('<c r="D{}"><f>2000+2000</f><v>4000</v></c>'.format(row_number))
            else:
                cells.append(inline_cell("D{}".format(row_number), comm_value))
        if aerospike_value:
            cells.append(inline_cell("E{}".format(row_number), aerospike_value))
        rows.append('<row r="{}">{}</row>'.format(row_number, "".join(cells)))

    worksheet = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">'
        '<sheetData>{}</sheetData></worksheet>'.format("".join(rows))
    )
    workbook = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" '
        'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
        '<sheets><sheet name="VDU" sheetId="1" r:id="rId1"/></sheets></workbook>'
    )
    relationships = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
        '<Relationship Id="rId1" '
        'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" '
        'Target="worksheets/sheet1.xml"/></Relationships>'
    )
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("xl/workbook.xml", workbook)
        archive.writestr("xl/_rels/workbook.xml.rels", relationships)
        archive.writestr("xl/worksheets/sheet1.xml", worksheet)


def baseline_arguments(path):
    return types.SimpleNamespace(
        input=path,
        sheet="VDU",
        header_row=1,
        attribute_column="B",
        service_start_column="D",
        cpu_default_unit="m",
        memory_default_unit="Mi",
    )


def expected(checker, value, resource_type, source_cell):
    return {
        "normalized": checker.parse_quantity(value, resource_type),
        "display": value,
        "resource_type": resource_type,
        "source_cell": source_cell,
    }


class QuantityTests(unittest.TestCase):
    def setUp(self):
        self.checker = load_checker()

    def test_normalizes_equivalent_cpu_and_memory_quantities(self):
        self.assertEqual(self.checker.parse_quantity("2000m", "cpu"), Decimal(2))
        self.assertEqual(self.checker.parse_quantity("2", "cpu"), Decimal(2))
        self.assertEqual(
            self.checker.parse_quantity("4096Mi", "memory"),
            self.checker.parse_quantity("4Gi", "memory"),
        )
        self.assertEqual(self.checker.clean_quantity("\u201c4Gi\u201d"), "4Gi")


class ExcelBaselineTests(unittest.TestCase):
    def setUp(self):
        self.checker = load_checker()

    def test_reads_main_and_init_resources_with_default_units(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "baseline.xlsx")
            write_workbook(path)
            baseline = self.checker.read_baseline(baseline_arguments(path))

        self.assertEqual(baseline["resource_rows"], 6)
        self.assertEqual(baseline["expected_count"], 10)
        self.assertEqual(baseline["formula_cells"], ["D5"])
        self.assertEqual(
            baseline["components"]["comm"]["container.requests.memory"]["normalized"],
            Decimal(4096) * Decimal(1024) ** 2,
        )
        self.assertEqual(
            baseline["components"]["aerospike"]["initContainer.requests.cpu"]["normalized"],
            Decimal(2),
        )

    def test_rejects_a_missing_resource_attribute(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "baseline.xlsx")
            write_workbook(path, omit_attribute="init_resources_mem")
            with self.assertRaisesRegex(self.checker.InputError, "missing resource attributes"):
                self.checker.read_baseline(baseline_arguments(path))

    def test_reads_optional_pv_storage_with_memory_default_unit(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "baseline.xlsx")
            write_workbook(path, include_pv=True)
            baseline = self.checker.read_baseline(baseline_arguments(path))

        storage = baseline["components"]["comm"]["persistentVolume.capacity.storage"]
        self.assertEqual(storage["normalized"], Decimal(10) * Decimal(1024) ** 3)
        self.assertEqual(storage["display"], "10240Mi")
        self.assertEqual(storage["source_cell"], "D8")


class AuditTests(unittest.TestCase):
    def setUp(self):
        self.checker = load_checker()

    def test_reports_matches_missing_and_extra_resources(self):
        workloads = [
            {
                "kind": "StatefulSet",
                "metadata": {"name": "comm", "labels": {}},
                "spec": {"template": {"spec": {
                    "containers": [{
                        "name": "comm",
                        "resources": {
                            "requests": {"cpu": "2", "memory": "4Gi"},
                            "limits": {"cpu": "4"},
                        },
                    }],
                    "initContainers": [{
                        "name": "comm-init",
                        "resources": {"requests": {"cpu": "500m"}},
                    }],
                }}},
            },
            {
                "kind": "Deployment",
                "metadata": {"name": "mm", "labels": {}},
                "spec": {"template": {"spec": {
                    "containers": [{"name": "mm", "resources": {}}],
                }}},
            },
        ]
        baseline = {
            "components": {
                "comm": {
                    "container.requests.cpu": expected(self.checker, "2000m", "cpu", "D2"),
                    "container.requests.memory": expected(self.checker, "4096Mi", "memory", "D3"),
                    "initContainer.requests.cpu": expected(self.checker, "500m", "cpu", "D4"),
                },
                "mm": {
                    "container.requests.cpu": expected(self.checker, "1000m", "cpu", "E2"),
                },
            },
            "expected_count": 4,
        }
        arguments = types.SimpleNamespace(
            namespace="pramf01",
            ignore_extra="",
            extra_policy="warn",
            show_pass=True,
        )
        original_snapshot = self.checker.workload_snapshot
        self.checker.workload_snapshot = lambda namespace: workloads
        try:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                stats = self.checker.audit(arguments, baseline, {})
        finally:
            self.checker.workload_snapshot = original_snapshot

        rendered = output.getvalue()
        self.assertEqual(stats["passed"], 3)
        self.assertEqual(stats["missing"], 1)
        self.assertEqual(stats["extra"], 1)
        self.assertEqual(stats["failures"], 1)
        self.assertIn(
            "[PASS] [MATCH] comm/container.requests.cpu expected=2000m actual=2 source=D2",
            rendered,
        )
        self.assertIn(
            "[PASS] [MATCH] comm/container.requests.memory expected=4096Mi actual=4Gi source=D3",
            rendered,
        )
        self.assertIn("[FAIL] [MISSING] mm/container.requests.cpu", rendered)
        self.assertIn("[WARN] [EXTRA] comm/container.limits.cpu", rendered)
        self.assertIn("SYSSETUP_REPORT ", rendered)
        self.assertIn('"component": "comm"', rendered)
        self.assertIn('"pass": 3', rendered)
        self.assertIn('"warn": 1', rendered)

    def test_multiple_init_containers_require_mapping(self):
        workload = {
            "kind": "StatefulSet",
            "metadata": {"name": "aerospike"},
            "spec": {"template": {"spec": {"initContainers": [
                {"name": "prepare"},
                {"name": "permissions"},
            ]}}},
        }
        with self.assertRaisesRegex(self.checker.KubernetesError, "use mapping_file"):
            self.checker.select_container("aerospike", workload, {}, init=True)
        selected = self.checker.select_container(
            "aerospike",
            workload,
            {"aerospike": {"name": "aerospike", "initContainer": "prepare"}},
            init=True,
        )
        self.assertEqual(selected["name"], "prepare")

    def test_checks_every_bound_pv_capacity_for_a_statefulset(self):
        workloads = [{
            "kind": "StatefulSet",
            "metadata": {"name": "database", "labels": {}},
            "spec": {
                "template": {
                    "spec": {
                        "containers": [{"name": "database", "resources": {}}],
                        "volumes": [{
                            "name": "data",
                            "persistentVolumeClaim": {"claimName": "data"},
                        }],
                    },
                },
                "volumeClaimTemplates": [{"metadata": {"name": "data"}}],
            },
        }]
        claims = [
            {"metadata": {"name": "data-database-0"}, "spec": {"volumeName": "pv-database-0"}},
            {"metadata": {"name": "data-database-1"}, "spec": {"volumeName": "pv-database-1"}},
        ]
        volumes = [
            {"metadata": {"name": "pv-database-0"}, "spec": {"capacity": {"storage": "10Gi"}}},
            {"metadata": {"name": "pv-database-1"}, "spec": {"capacity": {"storage": "10Gi"}}},
        ]
        baseline = {
            "components": {
                "database": {
                    "persistentVolume.capacity.storage": expected(self.checker, "10240Mi", "memory", "D8"),
                },
            },
            "expected_count": 1,
        }
        arguments = types.SimpleNamespace(
            namespace="pramf01", ignore_extra="", extra_policy="warn", show_pass=True
        )
        original_workloads = self.checker.workload_snapshot
        original_storage = self.checker.storage_snapshot
        self.checker.workload_snapshot = lambda namespace: workloads
        self.checker.storage_snapshot = lambda namespace: (claims, volumes)
        try:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                stats = self.checker.audit(arguments, baseline, {})
        finally:
            self.checker.workload_snapshot = original_workloads
            self.checker.storage_snapshot = original_storage

        rendered = output.getvalue()
        self.assertEqual(stats["passed"], 1)
        self.assertEqual(stats["failures"], 0)
        self.assertIn("expected=10240Mi", rendered)
        self.assertIn("10Gi (pvc=data-database-0, pv=pv-database-0)", rendered)
        self.assertIn("10Gi (pvc=data-database-1, pv=pv-database-1)", rendered)
        self.assertNotIn("data (PVC was not found)", rendered)

        volumes[1]["spec"]["capacity"]["storage"] = "20Gi"
        self.checker.workload_snapshot = lambda namespace: workloads
        self.checker.storage_snapshot = lambda namespace: (claims, volumes)
        try:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                stats = self.checker.audit(arguments, baseline, {})
        finally:
            self.checker.workload_snapshot = original_workloads
            self.checker.storage_snapshot = original_storage

        self.assertEqual(stats["mismatch"], 1)
        self.assertEqual(stats["failures"], 1)
        self.assertIn("[FAIL] [MISMATCH]", output.getvalue())
        self.assertIn("20Gi (pvc=data-database-1, pv=pv-database-1)", output.getvalue())


if __name__ == "__main__":
    unittest.main()
