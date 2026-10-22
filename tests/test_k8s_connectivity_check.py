import os
import pathlib
import shutil
import subprocess
import tempfile
import textwrap
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "features" / "k8s-connectivity-check" / "run.sh"


class KubernetesConnectivityCheckTest(unittest.TestCase):
    def setUp(self):
        git_bash = pathlib.Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "Git" / "bin" / "bash.exe"
        self.bash = str(git_bash) if os.name == "nt" and git_bash.is_file() else shutil.which("bash")
        if self.bash is None:
            self.skipTest("bash is not available")
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.bin_dir = pathlib.Path(self.temp_dir.name) / "bin"
        self.bin_dir.mkdir()
        self.event_log = pathlib.Path(self.temp_dir.name) / "events.log"
        kubectl = self.bin_dir / "kubectl"
        kubectl.write_text(
            textwrap.dedent(
                r"""#!/bin/bash
                set -u

                if [[ "$1" == get && "$2" == namespace ]]; then
                  exit 0
                fi

                if [[ "$1" == get && "$2" == pods ]]; then
                  printf '%s\n' mm-0 mm-controller-0 ipgw-0
                  exit 0
                fi

                if [[ "$1" == exec ]]; then
                  command_name="$6"
                  if [[ "$command_name" == curl && "${7:-}" == --version ]]; then
                    printf 'curl mock\n'
                    exit 0
                  fi

                  printf 'start %s\n' "$$" >> "$MOCK_EVENT_LOG"
                  sleep 0.5
                  if [[ "$command_name" == curl ]]; then
                    printf '\n200\n'
                  fi
                  printf 'end %s\n' "$$" >> "$MOCK_EVENT_LOG"
                  exit 0
                fi

                exit 1
                """
            ).lstrip(),
            encoding="utf-8",
        )
        kubectl.chmod(0o755)

    def environment(self, phase):
        environment = os.environ.copy()
        environment.update(
            {
                "PATH": str(self.bin_dir) + os.pathsep + environment.get("PATH", ""),
                "MOCK_EVENT_LOG": str(self.event_log),
                "NO_COLOR": "1",
                "SYSSETUP_PARAM_PHASE": phase,
                "SYSSETUP_PARAM_NAMESPACE": "test",
                "SYSSETUP_PARAM_IP_AUSF": "10.0.0.1",
                "SYSSETUP_PARAM_IP_UDM": "",
                "SYSSETUP_PARAM_IP_SMF": "",
                "SYSSETUP_PARAM_IP_AMF_SELF": "",
                "SYSSETUP_PARAM_IP_AMF_REMOTE": "",
                "SYSSETUP_PARAM_IP_NRF": "",
                "SYSSETUP_PARAM_IP_EIR": "",
                "SYSSETUP_PARAM_IP_NSSF": "",
                "SYSSETUP_PARAM_IP_GNODEB": "10.0.0.2",
                "SYSSETUP_PARAM_CURL_AUSF_URLS": "http://10.0.0.1/",
                "SYSSETUP_PARAM_CURL_UDM_URLS": "",
                "SYSSETUP_PARAM_CURL_SMF_URLS": "",
                "SYSSETUP_PARAM_CURL_AMF_SELF_URLS": "",
                "SYSSETUP_PARAM_CURL_AMF_REMOTE_URLS": "",
                "SYSSETUP_PARAM_CURL_NRF_URLS": "",
                "SYSSETUP_PARAM_CURL_EIR_URLS": "",
                "SYSSETUP_PARAM_CURL_NSSF_URLS": "",
                "SYSSETUP_PARAM_PARALLEL_INTERFACES": "true",
                "SYSSETUP_PARAM_MAX_PARALLEL_PODS": "10",
                "SYSSETUP_PARAM_MAX_PARALLEL_NFS_PER_POD": "1",
                "SYSSETUP_PARAM_MAX_TOTAL_PARALLEL_JOBS": "10",
            }
        )
        return environment

    def run_feature(self, phase):
        completed = subprocess.run(
            [self.bash, str(SCRIPT), "verify"],
            cwd=ROOT,
            env=self.environment(phase),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=15,
            check=False,
        )
        return completed.returncode, completed.stdout

    def assert_parallel_execution(self):
        events = self.event_log.read_text(encoding="utf-8").splitlines()
        active = 0
        maximum = 0
        for event in events:
            if event.startswith("start "):
                active += 1
                maximum = max(maximum, active)
            elif event.startswith("end "):
                active -= 1
        self.assertGreater(maximum, 1, events)
        self.assertLessEqual(maximum, 10, events)

    def test_ping_runs_mm_and_mm_controller_in_parallel(self):
        returncode, output = self.run_feature("ping")
        self.assertEqual(returncode, 0, output)
        self.assertIn("Pod [mm-0]: path to AUSF", output)
        self.assertIn("Pod [mm-controller-0]: path to AUSF", output)
        self.assertIn("Checklist completed: 5 check(s), 0 failure(s).", output)
        self.assert_parallel_execution()

    def test_curl_runs_mm_and_mm_controller_in_parallel(self):
        returncode, output = self.run_feature("curl")
        self.assertEqual(returncode, 0, output)
        self.assertIn("Pod [mm-0]: NF [AUSF]", output)
        self.assertIn("Pod [mm-controller-0]: NF [AUSF]", output)
        self.assertIn("Checklist completed: 2 check(s), 0 failure(s).", output)
        self.assert_parallel_execution()


if __name__ == "__main__":
    unittest.main()
