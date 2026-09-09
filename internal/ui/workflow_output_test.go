package ui

import "testing"

func TestDecorateWorkflowOutputHighlightsStatuses(t *testing.T) {
	input := "==> [service.1] check\n[PASS] ready\n[FAIL] broken\n[WARN] inspect\n[SKIPPED] next\nplain"
	want := workflowANSIInfo + "==> [service.1] check" + workflowANSIReset + "\n" +
		workflowANSIPass + "[PASS] ready" + workflowANSIReset + "\n" +
		workflowANSIFail + "[FAIL] broken" + workflowANSIReset + "\n" +
		workflowANSIWarn + "[WARN] inspect" + workflowANSIReset + "\n" +
		workflowANSIWarn + "[SKIPPED] next" + workflowANSIReset + "\nplain"
	if got := decorateWorkflowOutput(input, true); got != want {
		t.Fatalf("decorated output = %q, want %q", got, want)
	}
}

func TestDecorateWorkflowOutputCanDisableColors(t *testing.T) {
	input := "[PASS] ready\n[FAIL] broken"
	if got := decorateWorkflowOutput(input, false); got != input {
		t.Fatalf("disabled decoration changed output: %q", got)
	}
}
