package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/quyenhl16/script/internal/config"
	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/report"
	"github.com/quyenhl16/script/internal/runner"
	"github.com/quyenhl16/script/internal/ui"
	"github.com/quyenhl16/script/internal/workflow"
)

var version = "0.1.0"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	if args[0] == "version" {
		fmt.Println("syssetup", version)
		return nil
	}
	if args[0] == "reports" {
		return runReportCommand(ctx, args[1:])
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	featuresDir := flags.String("features-dir", envOrDefault("SYSSETUP_FEATURES_DIR", "features"), "directory containing feature packages")
	workflowsDir := flags.String("workflows-dir", envOrDefault("SYSSETUP_WORKFLOWS_DIR", "workflows"), "directory containing workflow packages")
	profilesDir := flags.String("profiles-dir", envOrDefault("SYSSETUP_PROFILES_DIR", "profiles"), "directory containing selectable profiles")
	profilePath := flags.String("profile", filepath.Join("profiles", "01HTX", "base-server.json"), "profile JSON file")
	dryRun := flags.Bool("dry-run", false, "show execution plan without changing the system")
	logPath := flags.String("log", "syssetup.log", "execution log file")
	reportFormatValue := flags.String("report-format", envOrDefault("SYSSETUP_REPORT_FORMAT", "md"), "report format: md, html, or xlsx")
	reportsDir := flags.String("reports-dir", envOrDefault("SYSSETUP_REPORTS_DIR", "reports"), "directory for generated reports")
	reportListen := flags.String("report-listen", envOrDefault("SYSSETUP_REPORT_LISTEN", "127.0.0.1:8080"), "TUI report web server listen address")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	reportFormat, err := report.ParseFormat(*reportFormatValue)
	if err != nil {
		return err
	}

	reg, err := registry.Load(filepath.Clean(*featuresDir))
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		for _, feature := range reg.List() {
			fmt.Printf("%-20s %-8s %s\n", feature.ID, feature.Version, feature.Description)
		}
		return nil
	case "workflows":
		workflows, err := workflow.Load(filepath.Clean(*workflowsDir), reg)
		if err != nil {
			return err
		}
		for _, definition := range workflows.List() {
			fmt.Printf("%-24s %-8s %d step(s)  %s\n", definition.ID, definition.Version, len(definition.Steps), definition.Description)
		}
		return nil
	case "plan", "run", "tui":
		profile, err := loadOptionalProfile(*profilePath, args[0] == "tui")
		if err != nil {
			return err
		}
		if args[0] == "tui" {
			workflows, err := workflow.Load(filepath.Clean(*workflowsDir), reg)
			if err != nil {
				return err
			}
			profiles, err := config.LoadProfiles(filepath.Clean(*profilesDir))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			profiles = includeProfile(profiles, profile)
			return ui.New(os.Stdin, os.Stdout).Run(ctx, reg, workflows, profiles, profile, runner.Options{
				DryRun: *dryRun, LogPath: *logPath, Output: os.Stdout,
				ReportFormat: string(reportFormat), ReportsDir: filepath.Clean(*reportsDir), ReportListen: strings.TrimSpace(*reportListen),
			})
		}
		resolved, err := reg.Resolve(profile)
		if err != nil {
			return err
		}
		if args[0] == "plan" {
			printPlan(profile, resolved)
			return nil
		}
		started := time.Now()
		var output bytes.Buffer
		executor, err := runner.New(runner.Options{DryRun: *dryRun, LogPath: *logPath, Output: io.MultiWriter(os.Stdout, &output)})
		if err != nil {
			reportPath, reportErr := report.Write(
				report.FeatureRun(profile, nil, output.String(), err, started, time.Now()),
				report.Options{Directory: filepath.Clean(*reportsDir), Format: reportFormat},
			)
			printReportPath(reportPath)
			return report.JoinRunAndReportErrors(err, reportErr)
		}
		results, runErr := executor.Execute(ctx, resolved)
		closeErr := executor.Close()
		finished := time.Now()
		reportPath, reportErr := report.Write(
			report.FeatureRun(profile, results, output.String(), errors.Join(runErr, closeErr), started, finished),
			report.Options{Directory: filepath.Clean(*reportsDir), Format: reportFormat},
		)
		printReportPath(reportPath)
		return report.JoinRunAndReportErrors(errors.Join(runErr, closeErr), reportErr)
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runReportCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("usage: syssetup reports serve [--listen ADDRESS] [--reports-dir PATH]")
	}
	flags := flag.NewFlagSet("reports serve", flag.ContinueOnError)
	listen := flags.String("listen", envOrDefault("SYSSETUP_REPORT_LISTEN", "127.0.0.1:8080"), "HTTP listen address")
	reportsDir := flags.String("reports-dir", envOrDefault("SYSSETUP_REPORTS_DIR", "reports"), "report root directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected report server argument %q", flags.Arg(0))
	}

	serveContext, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	err := report.Serve(serveContext, report.ServerOptions{
		Directory: filepath.Clean(*reportsDir),
		Listen:    strings.TrimSpace(*listen),
		Output:    os.Stdout,
	})
	if errors.Is(err, context.Canceled) && serveContext.Err() != nil {
		return nil
	}
	return err
}

func printReportPath(path string) {
	if path != "" {
		fmt.Println("Report:", path)
	}
}

func includeProfile(profiles []domain.Profile, profile domain.Profile) []domain.Profile {
	for index, existing := range profiles {
		if sameProfile(existing, profile) {
			profiles[index] = profile
			sortProfiles(profiles)
			return profiles
		}
	}
	profiles = append(profiles, profile)
	sortProfiles(profiles)
	return profiles
}

func sortProfiles(profiles []domain.Profile) {
	sort.Slice(profiles, func(i, j int) bool {
		leftSystem := strings.ToLower(profiles[i].System)
		rightSystem := strings.ToLower(profiles[j].System)
		if leftSystem != rightSystem {
			return leftSystem < rightSystem
		}
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
}

func sameProfile(left, right domain.Profile) bool {
	if left.Path != "" && right.Path != "" {
		leftPath, leftErr := filepath.Abs(left.Path)
		rightPath, rightErr := filepath.Abs(right.Path)
		if leftErr == nil && rightErr == nil && strings.EqualFold(leftPath, rightPath) {
			return true
		}
	}
	return strings.EqualFold(left.System, right.System) && strings.EqualFold(left.Name, right.Name)
}

func loadOptionalProfile(path string, optional bool) (domain.Profile, error) {
	profile, err := config.LoadProfile(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return domain.Profile{APIVersion: "syssetup/v1", Name: "interactive"}, nil
	}
	return profile, err
}

func printPlan(profile domain.Profile, features []domain.ResolvedFeature) {
	fmt.Printf("Profile: %s\n", profile.Name)
	for index, item := range features {
		root := "user"
		if item.Feature.RequireRoot {
			root = "root"
		}
		fmt.Printf("%2d. %-20s (%s, timeout=%ds)\n", index+1, item.Feature.ID, root, item.Feature.TimeoutSeconds)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func usage() {
	fmt.Println(`syssetup - extensible RHEL system setup tool

Usage:
  syssetup list [--features-dir PATH]
  syssetup workflows [--features-dir PATH] [--workflows-dir PATH]
  syssetup reports serve [--listen ADDRESS] [--reports-dir PATH]
  syssetup plan [--profile PATH]
  syssetup run  [--profile PATH] [--dry-run] [--log PATH] [--report-format md|html|xlsx] [--reports-dir PATH]
  syssetup tui  [--profile PATH] [--profiles-dir PATH] [--workflows-dir PATH] [--dry-run] [--log PATH] [--report-format md|html|xlsx] [--reports-dir PATH] [--report-listen ADDRESS]
  syssetup version`)
}
