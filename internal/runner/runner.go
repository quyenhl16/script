package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/platform"
)

const checkNeedsChange = 10

var unsafeEnvCharacters = regexp.MustCompile(`[^A-Z0-9_]`)

type Options struct {
	DryRun  bool
	LogPath string
	Output  io.Writer
}

type Runner struct {
	options Options
	osInfo  platform.Info
	logFile *os.File
}

func New(options Options) (*Runner, error) {
	if options.Output == nil {
		options.Output = os.Stdout
	}
	r := &Runner{options: options}
	if options.DryRun {
		return r, nil
	}
	info, err := platform.Detect()
	if err != nil {
		return nil, err
	}
	r.osInfo = info
	if options.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(options.LogPath), 0o755); err != nil && filepath.Dir(options.LogPath) != "." {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
		file, err := os.OpenFile(options.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open log: %w", err)
		}
		r.logFile = file
	}
	return r, nil
}

func (r *Runner) Close() error {
	if r.logFile != nil {
		return r.logFile.Close()
	}
	return nil
}

func (r *Runner) Execute(ctx context.Context, features []domain.ResolvedFeature) ([]domain.Result, error) {
	results := make([]domain.Result, 0, len(features))
	for _, feature := range features {
		result := r.executeFeature(ctx, feature)
		results = append(results, result)
		if result.Status == domain.StatusFailed {
			return results, fmt.Errorf("feature %q failed: %s", result.FeatureID, result.Message)
		}
	}
	return results, nil
}

func (r *Runner) executeFeature(ctx context.Context, item domain.ResolvedFeature) domain.Result {
	feature := item.Feature
	if r.options.DryRun {
		fmt.Fprintf(r.options.Output, "[PLAN] %-20s check -> apply -> verify\n", feature.ID)
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusPlanned, Message: "dry-run"}
	}
	if !r.osInfo.Supports(feature.SupportedOS) {
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusFailed, Message: "unsupported operating system: " + r.osInfo.ID}
	}
	if feature.RequireRoot && !platform.IsRoot() {
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusFailed, Message: "root privileges required"}
	}

	fmt.Fprintf(r.options.Output, "\n==> %s (%s)\n", feature.Name, feature.ID)
	checkErr := r.runAction(ctx, item, "check")
	if checkErr == nil {
		fmt.Fprintln(r.options.Output, "    already configured")
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusSkipped, Message: "already configured"}
	}
	var exitErr *exec.ExitError
	if !errors.As(checkErr, &exitErr) || exitErr.ExitCode() != checkNeedsChange {
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusFailed, Message: "check: " + checkErr.Error()}
	}
	if err := r.runAction(ctx, item, "apply"); err != nil {
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusFailed, Message: "apply: " + err.Error()}
	}
	if err := r.runAction(ctx, item, "verify"); err != nil {
		return domain.Result{FeatureID: feature.ID, Status: domain.StatusFailed, Message: "verify: " + err.Error()}
	}
	fmt.Fprintln(r.options.Output, "    done")
	return domain.Result{FeatureID: feature.ID, Status: domain.StatusDone, Message: "configured"}
}

func (r *Runner) runAction(parent context.Context, item domain.ResolvedFeature, action string) error {
	timeout := time.Duration(item.Feature.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	script := filepath.Join(item.Feature.Directory, item.Feature.Entrypoint)
	cmd := exec.CommandContext(ctx, "/usr/bin/env", "bash", script, action)
	cmd.Env = append(os.Environ(), parameterEnvironment(item.Parameters)...)
	output := r.options.Output
	if r.logFile != nil {
		output = io.MultiWriter(output, r.logFile)
		fmt.Fprintf(r.logFile, "%s feature=%s action=%s\n", time.Now().Format(time.RFC3339), item.Feature.ID, action)
	}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s", timeout)
	}
	return err
}

func parameterEnvironment(parameters map[string]any) []string {
	values := make([]string, 0, len(parameters))
	for name, value := range parameters {
		key := "SYSSETUP_PARAM_" + unsafeEnvCharacters.ReplaceAllString(strings.ToUpper(name), "_")
		var text string
		switch typed := value.(type) {
		case string:
			text = typed
		case bool:
			text = strconv.FormatBool(typed)
		case float64:
			text = strconv.FormatInt(int64(typed), 10)
		default:
			text = fmt.Sprint(typed)
		}
		values = append(values, key+"="+text)
	}
	return values
}
