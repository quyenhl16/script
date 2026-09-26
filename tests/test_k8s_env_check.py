import contextlib
import importlib.util
import io
import os
import tempfile
import types
import unittest
import zipfile


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CHECKER_PATH = os.path.join(ROOT, "features", "k8s-env-check", "check.py")


def load_checker():
    spec = importlib.util.spec_from_file_location("k8s_env_check", CHECKER_PATH)
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


def write_workbook(path, include_env_name=True):
    namespace_name = "NAMESPACE" if include_env_name else ""
    rows = [
        '<row r="1">{}</row>'.format("".join([
            inline_cell("B1", "Thuộc tính"),
            inline_cell("C1", "Tên ENV Kubernetes"),
            inline_cell("D1", "comm"),
            inline_cell("E1", "mm_controller"),
        ])),
        '<row r="2">{}</row>'.format("".join([
            inline_cell("B2", "environments_namespace"),
            inline_cell("C2", namespace_name),
            inline_cell("D2", "pramf01"),
            inline_cell("E2", "pramf01"),
        ])),
        '<row r="3">{}</row>'.format("".join([
            inline_cell("B3", "environments_log_level"),
            inline_cell("C3", "LOG_LEVEL"),
            inline_cell("D3", "debug"),
        ])),
        '<row r="4">{}</row>'.format("".join([
            inline_cell("B4", "environments_dynamic"),
            inline_cell("C4", "DYNAMIC_VALUE"),
            inline_cell("D4", "<PRESENT>"),
        ])),
        '<row r="5">{}{}</row>'.format(
            "".join([
                inline_cell("B5", "environments_count"),
                inline_cell("C5", "COUNT"),
            ]),
            '<c r="D5"><f>1+1</f><v>2</v></c>',
        ),
    ]
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
        env_name_column="C",
        service_start_column="D",
        attribute_prefix="environments_",
    )


class ExcelBaselineTests(unittest.TestCase):
    def setUp(self):
        self.checker = load_checker()

    def test_reads_service_environment_matrix_and_cached_formula(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "baseline.xlsx")
            write_workbook(path)
            baseline = self.checker.read_baseline(baseline_arguments(path))

        self.assertEqual(baseline["environment_rows"], 4)
        self.assertEqual(baseline["expected_count"], 5)
        self.assertEqual(baseline["formula_cells"], ["D5"])
        self.assertEqual(baseline["components"]["comm"]["NAMESPACE"]["value"], "pramf01")
        self.assertEqual(baseline["components"]["comm"]["DYNAMIC_VALUE"]["mode"], "present")
        self.assertEqual(baseline["components"]["comm"]["COUNT"]["value"], "2")

    def test_skips_populated_service_row_without_env_name(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "baseline.xlsx")
            write_workbook(path, include_env_name=False)
            baseline = self.checker.read_baseline(baseline_arguments(path))

        self.assertEqual(baseline["attribute_rows"], 4)
        self.assertEqual(baseline["environment_rows"], 3)
        self.assertEqual(baseline["skipped_unnamed_rows"], 1)
        self.assertEqual(baseline["expected_count"], 3)
        self.assertNotIn("NAMESPACE", baseline["components"]["comm"])
        self.assertNotIn("NAMESPACE", baseline["components"]["mm_controller"])


class AuditTests(unittest.TestCase):
    def setUp(self):
        self.checker = load_checker()

    def test_reports_expected_and_actual_for_mismatch(self):
        workloads = [
            {
                "kind": "Deployment",
                "metadata": {"name": "comm", "labels": {}},
                "spec": {"template": {"spec": {"containers": [{
                    "name": "comm",
                    "env": [
                        {"name": "NAMESPACE", "value": "pramf01"},
                        {"name": "LOG_LEVEL", "value": "debug"},
                        {"name": "EXTRA_ENV", "value": "must-not-be-printed"},
                    ],
                }]}}},
            },
            {
                "kind": "Deployment",
                "metadata": {"name": "mm-controller", "labels": {}},
                "spec": {"template": {"spec": {"containers": [{
                    "name": "mm-controller",
                    "env": [{"name": "NAMESPACE", "value": "wrong-value"}],
                }]}}},
            },
        ]
        baseline = {
            "components": {
                "comm": {
                    "NAMESPACE": {"mode": "exact", "value": "pramf01", "source_cell": "D2"},
                    "LOG_LEVEL": {"mode": "present", "source_cell": "D3"},
                },
                "mm_controller": {
                    "NAMESPACE": {"mode": "exact", "value": "pramf01", "source_cell": "E2"},
                },
            },
            "expected_count": 3,
        }
        arguments = types.SimpleNamespace(
            namespace="pramf01",
            ignore_extra="",
            compare_values=True,
            extra_policy="warn",
            show_pass=False,
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
        self.assertEqual(stats["passed"], 2)
        self.assertEqual(stats["mismatch"], 1)
        self.assertEqual(stats["extra"], 1)
        self.assertEqual(stats["failures"], 1)
        self.assertIn("[FAIL] [MISMATCH] mm_controller/NAMESPACE", rendered)
        self.assertIn('expected="pramf01" actual="wrong-value" source=E2', rendered)
        self.assertIn("[WARN] [EXTRA] comm/EXTRA_ENV", rendered)
        self.assertNotIn("must-not-be-printed", rendered)
        self.assertIn("SYSSETUP_REPORT ", rendered)
        self.assertIn('"component": "comm"', rendered)
        self.assertIn('"status": "WARN"', rendered)

    def test_masks_secret_value_in_mismatch_output(self):
        actual = self.checker.actual_value(
            "secret-value",
            kind="Secret",
            name="app-secret",
            key="password",
            sensitive=True,
        )

        rendered = self.checker.display_actual(actual)

        self.assertEqual(rendered, "<SECRET:app-secret/password>")
        self.assertNotIn("secret-value", rendered)

    def test_resolves_env_from_configmap_and_secret_key_reference(self):
        class FakeResolver:
            def __init__(self):
                self.issues = []

            def resource_values(self, kind, name, optional):
                values = {
                    ("ConfigMap", "app-config"): {
                        "HOST": {"value": "service.local", "sensitive": False},
                    },
                    ("Secret", "app-secret"): {
                        "password": {"value": "secret-value", "sensitive": True},
                    },
                }
                return values.get((kind, name), {})

        container = {
            "envFrom": [{"prefix": "APP_", "configMapRef": {"name": "app-config"}}],
            "env": [{
                "name": "PASSWORD",
                "valueFrom": {"secretKeyRef": {"name": "app-secret", "key": "password"}},
            }],
        }
        actual = self.checker.resolve_container_environment(container, FakeResolver())

        self.assertEqual(actual["APP_HOST"]["value"], "service.local")
        self.assertEqual(actual["APP_HOST"]["kind"], "ConfigMap")
        self.assertEqual(actual["PASSWORD"]["value"], "secret-value")
        self.assertTrue(actual["PASSWORD"]["sensitive"])


if __name__ == "__main__":
    unittest.main()
