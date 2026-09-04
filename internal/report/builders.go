package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/remote"
	"github.com/quyenhl16/script/internal/workflow"
)

func FeatureRun(profile domain.Profile, results []domain.Result, output string, runErr error, started, finished time.Time) Document {
	counts := make(map[domain.Status]int)
	entries := make([]Entry, 0, len(results)+1)
	for _, result := range results {
		counts[result.Status]++
		entries = append(entries, Entry{Title: result.FeatureID, Status: string(result.Status), Message: result.Message})
	}
	status := "PASS"
	if counts[domain.StatusPlanned] == len(results) && len(results) > 0 {
		status = "PLANNED"
	} else if runErr != nil || counts[domain.StatusFailed] > 0 {
		status = "FAILED"
		if len(results) > counts[domain.StatusFailed] && counts[domain.StatusFailed] > 0 {
			status = "PARTIAL"
		}
	}
	if errors.Is(runErr, context.Canceled) {
		status = "CANCELLED"
	}
	if output != "" {
		entries = append(entries, Entry{Title: "Execution output", Status: status, Output: output})
	}
	return Document{
		Title: "SYSSETUP FEATURE REPORT", Operation: "features-" + profile.Name,
		System: profile.System, Profile: profile.Name, Kind: "feature run", Status: status,
		StartedAt: started, FinishedAt: finished,
		Summary: []SummaryItem{
			{Label: "Done", Value: fmt.Sprint(counts[domain.StatusDone])},
			{Label: "Skipped", Value: fmt.Sprint(counts[domain.StatusSkipped])},
			{Label: "Planned", Value: fmt.Sprint(counts[domain.StatusPlanned])},
			{Label: "Failed", Value: fmt.Sprint(counts[domain.StatusFailed])},
		},
		Entries: entries,
	}
}

func RemoteRun(profile domain.Profile, operation, kind string, results []remote.Result, started, finished time.Time) Document {
	passed := 0
	cancelled := false
	entries := make([]Entry, 0, len(results))
	for _, result := range results {
		status := "PASS"
		message := ""
		if result.Err != nil {
			status = "FAILED"
			message = result.Err.Error()
			cancelled = cancelled || errors.Is(result.Err, context.Canceled)
		} else {
			passed++
		}
		entries = append(entries, Entry{
			Title: result.Address, Status: status,
			Duration: result.Duration, Message: message, Output: result.Output,
		})
	}
	status := aggregateStatus(passed, len(results))
	if cancelled {
		status = "CANCELLED"
	}
	return Document{
		Title: "SYSSETUP REMOTE REPORT", Operation: operation,
		System: profile.System, Profile: profile.Name, Kind: kind, Status: status,
		StartedAt: started, FinishedAt: finished,
		Summary: []SummaryItem{
			{Label: "Servers", Value: fmt.Sprint(len(results))},
			{Label: "Passed", Value: fmt.Sprint(passed)},
			{Label: "Failed", Value: fmt.Sprint(len(results) - passed)},
		},
		Entries: entries,
	}
}

func WorkflowRun(profile domain.Profile, definition workflow.Definition, execution workflow.Execution, runErr error, started, finished time.Time) Document {
	passedServers := 0
	for _, server := range execution.Servers {
		if server.Success {
			passedServers++
		}
	}
	entries := make([]Entry, 0, len(execution.Results))
	for _, result := range execution.Results {
		invocation := ""
		if result.Invocation > 0 {
			invocation = fmt.Sprintf(".%d", result.Invocation)
		}
		message := ""
		if result.Err != nil {
			message = result.Err.Error()
		}
		entries = append(entries, Entry{
			Title:  result.StepID + invocation + " - " + result.FeatureID,
			Target: result.Server, Status: string(result.Status), Duration: result.Duration,
			Message: message, Output: result.Output,
		})
	}
	status := aggregateStatus(passedServers, len(execution.Servers))
	if errors.Is(runErr, context.Canceled) {
		status = "CANCELLED"
	} else if runErr != nil && status == "PASS" {
		status = "FAILED"
	}
	return Document{
		Title: "SYSSETUP WORKFLOW REPORT", Operation: definition.ID,
		System: profile.System, Profile: profile.Name, Kind: "workflow", Status: status,
		StartedAt: started, FinishedAt: finished,
		Summary: []SummaryItem{
			{Label: "Servers", Value: fmt.Sprint(len(execution.Servers))},
			{Label: "Passed", Value: fmt.Sprint(passedServers)},
			{Label: "Failed", Value: fmt.Sprint(len(execution.Servers) - passedServers)},
			{Label: "Steps", Value: fmt.Sprint(len(execution.Results))},
		},
		Entries: entries,
	}
}

func aggregateStatus(passed, total int) string {
	if total == 0 || passed == 0 {
		return "FAILED"
	}
	if passed == total {
		return "PASS"
	}
	return "PARTIAL"
}
