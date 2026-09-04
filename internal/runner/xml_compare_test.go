package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestXMLCompareReportsCountsAndCriticalDifferences(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.xml")
	target := filepath.Join(root, "target.xml")
	rules := filepath.Join(root, "rules.json")
	writeXMLCompareFixture(t, source, `<config><amf><important><id>1</id><value>A</value></important><normal>x</normal><source-only>s</source-only></amf></config>`)
	writeXMLCompareFixture(t, target, `<config><amf><important><id>1</id><value>B</value></important><normal>x</normal><target-only>t</target-only></amf></config>`)
	writeXMLCompareFixture(t, rules, `{
  "apiVersion":"syssetup/xml-check/v1",
  "checks":[{
    "id":"important",
    "xpath":"/*[local-name()='config']/*[local-name()='amf']/*[local-name()='important']",
    "reportOnly":true,
    "required":true
  }]
}`)

	script := filepath.Join("..", "..", "features", "compare-xml-config", "compare.py")
	command := pythonCommand(t, script, source, target, rules, "false")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected differences to return a non-zero exit code")
	}
	text := string(output)
	for _, expected := range []string{
		"total_paths=5 same=2 different=3 changed=1 only_source=1 only_target=1",
		"Critical summary: rules=1 matched_rules=1 total_paths=2 same=1 different=1",
		`[CRITICAL:important] [CHANGED] /config/amf/important/value`,
		"[ONLY_SOURCE] /config/amf/source-only",
		"[ONLY_TARGET] /config/amf/target-only",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("comparison output does not contain %q:\n%s", expected, text)
		}
	}

	command = pythonCommand(t, script, source, source, rules, "false")
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("identical XML comparison failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "total_paths=4 same=4 different=0") {
		t.Fatalf("unexpected identical comparison output:\n%s", output)
	}
}

func TestXMLCompareUsesStableListKeysAcrossOrderAndNamespacePrefixes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.xml")
	target := filepath.Join(root, "target.xml")
	rules := filepath.Join(root, "rules.json")
	writeXMLCompareFixture(t, source, `<c:config xmlns:c="urn:config" xmlns:a="urn:amf"><a:amf><a:item><a:id>1</a:id><a:value>A</a:value></a:item><a:item><a:id>2</a:id><a:value>B</a:value></a:item></a:amf></c:config>`)
	writeXMLCompareFixture(t, target, `<config xmlns="urn:config"><amf xmlns="urn:amf"><item><id>2</id><value>B</value></item><item><id>1</id><value>A</value></item></amf></config>`)
	writeXMLCompareFixture(t, rules, `{
  "apiVersion":"syssetup/xml-check/v1",
  "checks":[{
    "id":"items",
    "xpath":"/*[local-name()='config']/*[local-name()='amf']/*[local-name()='item']",
    "reportOnly":true,
    "required":true
  }]
}`)

	script := filepath.Join("..", "..", "features", "compare-xml-config", "compare.py")
	output, err := pythonCommand(t, script, source, target, rules, "false").CombinedOutput()
	if err != nil {
		t.Fatalf("reordered keyed list comparison failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "total_paths=4 same=4 different=0") || !strings.Contains(text, `item[id="1"]/value`) {
		t.Fatalf("unexpected reordered comparison output:\n%s", text)
	}
}

func pythonCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	name := "python3"
	if runtime.GOOS == "windows" {
		name = "py"
		arguments = append([]string{"-3"}, arguments...)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not available: %v", name, err)
	}
	return exec.Command(path, arguments...)
}

func writeXMLCompareFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
