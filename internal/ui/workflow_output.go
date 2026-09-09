package ui

import (
	"os"
	"strings"
)

const (
	workflowANSIReset = "\x1b[0m"
	workflowANSIPass  = "\x1b[1;32m"
	workflowANSIFail  = "\x1b[1;31m"
	workflowANSIWarn  = "\x1b[1;33m"
	workflowANSIInfo  = "\x1b[1;36m"
)

func highlightWorkflowOutput(value string) string {
	_, disabled := os.LookupEnv("NO_COLOR")
	return decorateWorkflowOutput(value, !disabled)
}

func decorateWorkflowOutput(value string, enabled bool) string {
	if !enabled || value == "" {
		return value
	}
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		color := workflowLineColor(line)
		if color == "" || line == "" {
			continue
		}
		carriageReturn := ""
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
			carriageReturn = "\r"
		}
		lines[index] = color + line + workflowANSIReset + carriageReturn
	}
	return strings.Join(lines, "\n")
}

func workflowLineColor(line string) string {
	switch {
	case strings.Contains(line, "[FAIL]"),
		strings.Contains(line, "[FAILED]"),
		strings.Contains(line, "[ERROR]"):
		return workflowANSIFail
	case strings.Contains(line, "[PASS]"),
		strings.Contains(line, "[DONE]"):
		return workflowANSIPass
	case strings.Contains(line, "[WARN]"),
		strings.Contains(line, "[SKIP]"),
		strings.Contains(line, "[SKIPPED]"):
		return workflowANSIWarn
	case strings.HasPrefix(strings.TrimSpace(line), "==>"),
		strings.Contains(line, "[INFO]"):
		return workflowANSIInfo
	default:
		return ""
	}
}
