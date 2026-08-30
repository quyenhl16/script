package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/quyenhl16/script/internal/config"
	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
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

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	featuresDir := flags.String("features-dir", envOrDefault("SYSSETUP_FEATURES_DIR", "features"), "directory containing feature packages")
	workflowsDir := flags.String("workflows-dir", envOrDefault("SYSSETUP_WORKFLOWS_DIR", "workflows"), "directory containing workflow packages")
	profilePath := flags.String("profile", "profiles/base-server.json", "profile JSON file")
	dryRun := flags.Bool("dry-run", false, "show execution plan without changing the system")
	logPath := flags.String("log", "syssetup.log", "execution log file")
	if err := flags.Parse(args[1:]); err != nil {
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
			return ui.New(os.Stdin, os.Stdout).Run(ctx, reg, workflows, profile, runner.Options{DryRun: *dryRun, LogPath: *logPath, Output: os.Stdout})
		}
		resolved, err := reg.Resolve(profile)
		if err != nil {
			return err
		}
		if args[0] == "plan" {
			printPlan(profile, resolved)
			return nil
		}
		executor, err := runner.New(runner.Options{DryRun: *dryRun, LogPath: *logPath, Output: os.Stdout})
		if err != nil {
			return err
		}
		defer executor.Close()
		_, err = executor.Execute(ctx, resolved)
		return err
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
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
  syssetup plan [--profile PATH]
  syssetup run  [--profile PATH] [--dry-run] [--log PATH]
  syssetup tui  [--profile PATH] [--workflows-dir PATH] [--dry-run] [--log PATH]
  syssetup version`)
}
