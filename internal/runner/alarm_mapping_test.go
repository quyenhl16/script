package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type alarmMappingItem struct {
	PodPhase string `json:"PodPhase"`
	PodName  string `json:"PodName"`
	AlarmID  int    `json:"AlarmId"`
}

func TestAlarmMappingSenderSendsEveryRequestInOrder(t *testing.T) {
	requireCurl(t)
	var (
		mu       sync.Mutex
		received []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", request.Method)
		}
		if request.URL.Path != "/nnm-service/v1/updatealarm/AMF" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", request.Header.Get("Content-Type"))
		}
		var payload []alarmMappingItem
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(payload) != 1 {
			t.Errorf("payload length = %d, want 1", len(payload))
		} else {
			mu.Lock()
			received = append(received, payload[0].PodName)
			mu.Unlock()
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dataFile := writeAlarmMappingFixture(t, `{
  "apiVersion":"syssetup/alarm-mappings/v1",
  "requests":[
    {"name":"first","payload":[{"PodPhase":"1","PodName":"first","AlarmId":101}]},
    {"name":"second","payload":[{"PodPhase":"1","PodName":"second","AlarmId":102}]}
  ]
}`)
	script := filepath.Join("..", "..", "features", "update-alarm-mappings", "send.py")
	output, err := pythonCommand(t, script, dataFile, server.URL+"/nnm-service/v1/updatealarm/AMF", "2", "5").CombinedOutput()
	if err != nil {
		t.Fatalf("alarm mapping sender failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, expected := range []string{"[PASS] first: HTTP 204", "[PASS] second: HTTP 204", "[PASS] Summary: total=2 passed=2 failed=0"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("sender output does not contain %q:\n%s", expected, text)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(received, []string{"first", "second"}) {
		t.Fatalf("request order = %v", received)
	}
}

func TestAlarmMappingSenderContinuesAfterHTTPFailure(t *testing.T) {
	requireCurl(t)
	var (
		mu       sync.Mutex
		received []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload []alarmMappingItem
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload) == 0 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		name := payload[0].PodName
		mu.Lock()
		received = append(received, name)
		mu.Unlock()
		if name == "first" {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(writer, "temporary failure")
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dataFile := writeAlarmMappingFixture(t, `{
  "apiVersion":"syssetup/alarm-mappings/v1",
  "requests":[
    {"name":"first","payload":[{"PodPhase":"1","PodName":"first","AlarmId":101}]},
    {"name":"second","payload":[{"PodPhase":"1","PodName":"second","AlarmId":102}]}
  ]
}`)
	script := filepath.Join("..", "..", "features", "update-alarm-mappings", "send.py")
	output, err := pythonCommand(t, script, dataFile, server.URL, "2", "5").CombinedOutput()
	if err == nil {
		t.Fatalf("expected HTTP failure to produce a non-zero exit code:\n%s", output)
	}
	text := string(output)
	for _, expected := range []string{"[FAIL] first: HTTP 500", "temporary failure", "[PASS] second: HTTP 200", "[FAIL] Summary: total=2 passed=1 failed=1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("sender output does not contain %q:\n%s", expected, text)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(received, []string{"first", "second"}) {
		t.Fatalf("sender stopped early; request order = %v", received)
	}
}

func requireCurl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl is not available: %v", err)
	}
}

func writeAlarmMappingFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "alarm-mappings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
