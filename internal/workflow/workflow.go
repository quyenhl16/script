package workflow

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/remote"
)

const (
	definitionAPIVersion = "syssetup/workflow/v1"
	configAPIVersion     = "syssetup/workflow-config/v1"
)

type Definition struct {
	APIVersion    string `json:"apiVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Description   string `json:"description,omitempty"`
	ExecutionMode string `json:"executionMode,omitempty"`
	FailurePolicy string `json:"failurePolicy,omitempty"`
	Steps         []Step `json:"steps"`
	Directory     string `json:"-"`
}

type Step struct {
	ID         string      `json:"id"`
	Feature    string      `json:"feature"`
	Needs      []string    `json:"needs,omitempty"`
	DeriveArgs *DeriveArgs `json:"deriveArgs,omitempty"`
}

type DeriveArgs struct {
	Step          string `json:"step"`
	Prefix        string `json:"prefix,omitempty"`
	ArgumentIndex *int   `json:"argumentIndex,omitempty"`
	StripPrefix   bool   `json:"stripPrefix,omitempty"`
	RequireEach   bool   `json:"requireEach,omitempty"`
}

type Config struct {
	APIVersion string                  `json:"apiVersion"`
	Workflow   string                  `json:"workflow"`
	Steps      map[string][]Invocation `json:"steps"`
	Directory  string                  `json:"-"`
}

type Invocation struct {
	Args      []string            `json:"args"`
	Artifacts []ArtifactReference `json:"artifacts,omitempty"`
}

type ArtifactReference struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type Registry struct {
	definitions map[string]Definition
	features    *registry.Registry
}

type Status string

const (
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

type Result struct {
	Server     string
	StepID     string
	FeatureID  string
	Invocation int
	Status     Status
	Output     string
	Duration   time.Duration
	Err        error
}

type ServerResult struct {
	Address string
	Success bool
}

type Execution struct {
	Results []Result
	Servers []ServerResult
}

type preparedStep struct {
	definition  Step
	script      []byte
	invocations []preparedInvocation
	timeout     time.Duration
}

type preparedInvocation struct {
	args      []string
	artifacts []remote.Artifact
}

type serverExecution struct {
	results []Result
	success bool
}

func Load(root string, features *registry.Registry) (*Registry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read workflow directory %q: %w", root, err)
	}
	result := &Registry{definitions: make(map[string]Definition), features: features}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		path := filepath.Join(directory, "workflow.json")
		data, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("read %q: %w", path, readErr)
		}
		var definition Definition
		if err := json.Unmarshal(data, &definition); err != nil {
			return nil, fmt.Errorf("parse %q: %w", path, err)
		}
		definition.Directory = directory
		if err := validateDefinition(definition, features); err != nil {
			return nil, fmt.Errorf("workflow %q: %w", entry.Name(), err)
		}
		if _, found := result.definitions[definition.ID]; found {
			return nil, fmt.Errorf("duplicate workflow ID %q", definition.ID)
		}
		result.definitions[definition.ID] = definition
	}
	if len(result.definitions) == 0 {
		return nil, fmt.Errorf("no workflows found in %q", root)
	}
	return result, nil
}

func validateDefinition(definition Definition, features *registry.Registry) error {
	if definition.APIVersion != definitionAPIVersion {
		return fmt.Errorf("unsupported apiVersion %q", definition.APIVersion)
	}
	if definition.ID == "" || definition.Name == "" || definition.Version == "" {
		return errors.New("id, name and version are required")
	}
	switch definition.ExecutionMode {
	case "", "remote", "local":
	default:
		return fmt.Errorf("unsupported executionMode %q", definition.ExecutionMode)
	}
	switch definition.FailurePolicy {
	case "", "stop-server", "continue":
	default:
		return fmt.Errorf("unsupported failurePolicy %q", definition.FailurePolicy)
	}
	if len(definition.Steps) == 0 {
		return errors.New("at least one step is required")
	}
	seen := make(map[string]bool)
	for _, step := range definition.Steps {
		if step.ID == "" || step.Feature == "" {
			return errors.New("every step requires id and feature")
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate step ID %q", step.ID)
		}
		for _, dependency := range step.Needs {
			if !seen[dependency] {
				return fmt.Errorf("step %q needs unknown or later step %q", step.ID, dependency)
			}
		}
		feature, found := features.Get(step.Feature)
		if !found {
			return fmt.Errorf("step %q references unknown feature %q", step.ID, step.Feature)
		}
		if definition.ExecutionMode == "local" && (feature.RemoteOnly || !feature.WorkflowCompatible) {
			return fmt.Errorf("step %q feature %q is not compatible with local workflows", step.ID, step.Feature)
		}
		if definition.ExecutionMode != "local" && !feature.RemoteOnly && !feature.WorkflowCompatible {
			return fmt.Errorf("step %q feature %q must be remote-only or workflow-compatible", step.ID, step.Feature)
		}
		if step.DeriveArgs != nil {
			if !seen[step.DeriveArgs.Step] {
				return fmt.Errorf("step %q derives arguments from unknown or later step %q", step.ID, step.DeriveArgs.Step)
			}
			hasPrefix := step.DeriveArgs.Prefix != ""
			hasIndex := step.DeriveArgs.ArgumentIndex != nil
			if hasPrefix == hasIndex {
				return fmt.Errorf("step %q deriveArgs requires exactly one of prefix or argumentIndex", step.ID)
			}
			if hasIndex && *step.DeriveArgs.ArgumentIndex < 0 {
				return fmt.Errorf("step %q deriveArgs.argumentIndex must not be negative", step.ID)
			}
			if hasIndex && step.DeriveArgs.StripPrefix {
				return fmt.Errorf("step %q deriveArgs.stripPrefix requires prefix", step.ID)
			}
			if !contains(step.Needs, step.DeriveArgs.Step) {
				return fmt.Errorf("step %q must depend on argument source step %q", step.ID, step.DeriveArgs.Step)
			}
		}
		seen[step.ID] = true
	}
	return nil
}

func (r *Registry) List() []Definition {
	definitions := make([]Definition, 0, len(r.definitions))
	for _, definition := range r.definitions {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool {
		return strings.ToLower(definitions[i].ID) < strings.ToLower(definitions[j].ID)
	})
	return definitions
}

func (r *Registry) Get(id string) (Definition, bool) {
	definition, found := r.definitions[id]
	return definition, found
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("read workflow config: %w", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parse workflow config: %w", err)
	}
	if config.APIVersion != configAPIVersion {
		return Config{}, fmt.Errorf("unsupported workflow config apiVersion %q", config.APIVersion)
	}
	if config.Workflow == "" {
		return Config{}, errors.New("workflow config requires workflow")
	}
	config.Directory = filepath.Dir(filepath.Clean(path))
	return config, nil
}

func (r *Registry) ValidateConfig(definition Definition, config Config) error {
	if config.Workflow != definition.ID {
		return fmt.Errorf("config is for workflow %q, expected %q", config.Workflow, definition.ID)
	}
	known := make(map[string]Step, len(definition.Steps))
	for _, step := range definition.Steps {
		known[step.ID] = step
		if step.DeriveArgs == nil && len(config.Steps[step.ID]) == 0 {
			return fmt.Errorf("step %q requires at least one invocation", step.ID)
		}
	}
	for stepID, invocations := range config.Steps {
		step, found := known[stepID]
		if !found {
			return fmt.Errorf("config contains unknown step %q", stepID)
		}
		if step.DeriveArgs != nil && len(invocations) > 0 {
			return fmt.Errorf("derived step %q must not define invocations", stepID)
		}
		for index, invocation := range invocations {
			if len(invocation.Args) == 0 {
				return fmt.Errorf("step %q invocation %d has no arguments", stepID, index+1)
			}
			seenArtifacts := make(map[string]bool)
			for _, artifact := range invocation.Artifacts {
				if artifact.ID == "" || strings.TrimSpace(artifact.Source) == "" {
					return fmt.Errorf("step %q invocation %d artifact requires id and source", stepID, index+1)
				}
				if seenArtifacts[artifact.ID] {
					return fmt.Errorf("step %q invocation %d has duplicate artifact %q", stepID, index+1, artifact.ID)
				}
				seenArtifacts[artifact.ID] = true
			}
		}
	}
	for _, step := range definition.Steps {
		if step.DeriveArgs != nil {
			if _, err := deriveInvocations(step, config); err != nil {
				return err
			}
		}
	}
	return nil
}

// ApplyProfiles resolves every workflow step from the profile which configures
// its feature in the selected system. Profile parameters replace matching
// key=value arguments from the workflow config, so feature configuration has a
// single source of truth. Positional arguments and artifacts remain intact.
func (r *Registry) ApplyProfiles(definition Definition, config Config, profiles []domain.Profile, system string) (Config, error) {
	for _, step := range definition.Steps {
		if step.DeriveArgs != nil {
			continue
		}
		selection, found, err := profileSelection(profiles, system, step.Feature)
		if err != nil {
			return Config{}, fmt.Errorf("step %q: %w", step.ID, err)
		}
		if !found {
			continue
		}
		parameters, err := r.features.ResolveFeatureParameters(step.Feature, selection.Parameters)
		if err != nil {
			return Config{}, fmt.Errorf("step %q: %w", step.ID, err)
		}
		feature, _ := r.features.Get(step.Feature)
		invocations := config.Steps[step.ID]
		if len(invocations) == 0 {
			invocation := Invocation{}
			if !feature.RemoteOnly {
				invocation.Args = []string{"verify"}
			}
			invocations = []Invocation{invocation}
		}
		for index := range invocations {
			invocations[index].Args = mergeParameterArguments(invocations[index].Args, parameters)
			artifacts, err := resolveArtifactParameters(invocations[index].Artifacts, parameters)
			if err != nil {
				return Config{}, fmt.Errorf("step %q invocation %d: %w", step.ID, index+1, err)
			}
			invocations[index].Artifacts = artifacts
		}
		if config.Steps == nil {
			config.Steps = make(map[string][]Invocation)
		}
		config.Steps[step.ID] = invocations
	}
	return config, nil
}

// resolveArtifactParameters substitutes "@<parameter>" artifact sources with
// the resolved profile parameter value. Input workbooks and other per-system
// files live in profiles/<system>/; the workflow config stays structural by
// referencing them symbolically (e.g. "@counter_input_file"). Non-"@"
// sources pass through unchanged, so relative checker paths keep working.
func resolveArtifactParameters(references []ArtifactReference, parameters map[string]any) ([]ArtifactReference, error) {
	if len(references) == 0 {
		return references, nil
	}
	resolved := make([]ArtifactReference, len(references))
	for index, reference := range references {
		source := strings.TrimSpace(reference.Source)
		if strings.HasPrefix(source, "@") {
			name := strings.TrimPrefix(source, "@")
			value, exists := parameters[name]
			if !exists {
				return nil, fmt.Errorf("artifact %q references undefined parameter %q", reference.ID, name)
			}
			source = parameterString(value)
		}
		resolved[index] = ArtifactReference{ID: reference.ID, Source: source}
	}
	return resolved, nil
}

// invocationOutputDir reports the output_dir= argument of an invocation, the
// remote directory whose files are fetched back to the local machine after a
// remote run.
func invocationOutputDir(args []string) (string, bool) {
	for _, arg := range args {
		if value, found := strings.CutPrefix(arg, "output_dir="); found {
			return value, true
		}
	}
	return "", false
}

func profileSelection(profiles []domain.Profile, system, featureID string) (domain.FeatureSelection, bool, error) {
	var matches []struct {
		profile   domain.Profile
		selection domain.FeatureSelection
	}
	for _, profile := range profiles {
		if !strings.EqualFold(profile.System, system) {
			continue
		}
		for _, selection := range profile.Features {
			if selection.ID == featureID {
				matches = append(matches, struct {
					profile   domain.Profile
					selection domain.FeatureSelection
				}{profile: profile, selection: selection})
			}
		}
	}
	for _, match := range matches {
		if strings.EqualFold(match.profile.Name, featureID) {
			return match.selection, true, nil
		}
	}
	if len(matches) == 1 {
		return matches[0].selection, true, nil
	}
	if len(matches) > 1 {
		return domain.FeatureSelection{}, false, fmt.Errorf("multiple profiles configure feature %q; name one profile %q", featureID, featureID)
	}
	return domain.FeatureSelection{}, false, nil
}

func mergeParameterArguments(arguments []string, parameters map[string]any) []string {
	result := make([]string, 0, len(arguments)+len(parameters))
	for _, argument := range arguments {
		name, _, keyed := strings.Cut(argument, "=")
		if _, replaced := parameters[name]; keyed && replaced {
			continue
		}
		result = append(result, argument)
	}
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result = append(result, name+"="+parameterString(parameters[name]))
	}
	return result
}

func parameterString(value any) string {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(value)
	}
}

func deriveInvocations(step Step, config Config) ([]Invocation, error) {
	if step.DeriveArgs == nil {
		return config.Steps[step.ID], nil
	}
	seen := make(map[string]bool)
	var arguments []string
	for _, source := range config.Steps[step.DeriveArgs.Step] {
		matched := 0
		if step.DeriveArgs.ArgumentIndex != nil {
			index := *step.DeriveArgs.ArgumentIndex
			if index < len(source.Args) {
				value := source.Args[index]
				if value != "" && !seen[value] {
					seen[value] = true
					arguments = append(arguments, value)
				}
				matched = 1
			}
		} else {
			for _, argument := range source.Args {
				if !strings.HasPrefix(argument, step.DeriveArgs.Prefix) {
					continue
				}
				value := argument
				if step.DeriveArgs.StripPrefix {
					value = strings.TrimPrefix(argument, step.DeriveArgs.Prefix)
				}
				if value != "" && !seen[value] {
					seen[value] = true
					arguments = append(arguments, value)
				}
				matched++
			}
		}
		if step.DeriveArgs.RequireEach && matched == 0 {
			return nil, fmt.Errorf("step %q source invocation has no %s", step.ID, deriveArgsSelector(*step.DeriveArgs))
		}
	}
	if len(arguments) == 0 {
		return nil, fmt.Errorf("step %q could not derive arguments using %s from step %q", step.ID, deriveArgsSelector(*step.DeriveArgs), step.DeriveArgs.Step)
	}
	return []Invocation{{Args: arguments}}, nil
}

func deriveArgsSelector(derive DeriveArgs) string {
	if derive.ArgumentIndex != nil {
		return fmt.Sprintf("argument at index %d", *derive.ArgumentIndex)
	}
	return fmt.Sprintf("argument with prefix %q", derive.Prefix)
}

func (r *Registry) Execute(ctx context.Context, definition Definition, config Config, servers []remote.Server, hostKeys ssh.HostKeyCallback, liveOutputs ...io.Writer) (Execution, error) {
	if err := r.ValidateConfig(definition, config); err != nil {
		return Execution{}, err
	}
	liveOutput := io.Discard
	if len(liveOutputs) > 0 && liveOutputs[0] != nil {
		liveOutput = liveOutputs[0]
	}
	if definition.ExecutionMode == "local" {
		return r.executeLocal(ctx, definition, config, liveOutput)
	}
	prepared := make([]preparedStep, 0, len(definition.Steps))
	for _, step := range definition.Steps {
		feature, _ := r.features.Get(step.Feature)
		script, err := remote.LoadScript(filepath.Join(feature.Directory, feature.Entrypoint))
		if err != nil {
			return Execution{}, fmt.Errorf("load step %q script: %w", step.ID, err)
		}
		invocations, err := deriveInvocations(step, config)
		if err != nil {
			return Execution{}, err
		}
		preparedInvocations := make([]preparedInvocation, 0, len(invocations))
		for invocationIndex, invocation := range invocations {
			preparedInvocation := preparedInvocation{args: invocation.Args}
			for _, reference := range invocation.Artifacts {
				source := reference.Source
				if !filepath.IsAbs(source) {
					source = filepath.Join(config.Directory, source)
				}
				artifact, loadErr := remote.LoadArtifact(reference.ID, source)
				if loadErr != nil {
					return Execution{}, fmt.Errorf("load step %q invocation %d: %w", step.ID, invocationIndex+1, loadErr)
				}
				preparedInvocation.artifacts = append(preparedInvocation.artifacts, artifact)
			}
			preparedInvocations = append(preparedInvocations, preparedInvocation)
		}
		prepared = append(prepared, preparedStep{
			definition: step, script: script, invocations: preparedInvocations,
			timeout: time.Duration(feature.TimeoutSeconds) * time.Second,
		})
	}

	// Report files land under the directory that contains the workflow-configs
	// directory (repo root for the shipped layout), so relative output_dir
	// values such as ./reports/<system>/<name> resolve the same locally as on
	// the server.
	localOutputRoot := filepath.Clean(filepath.Join(config.Directory, ".."))
	serverExecutions := make([]serverExecution, len(servers))
	var group sync.WaitGroup
	for index, server := range servers {
		group.Add(1)
		go func() {
			defer group.Done()
			serverExecutions[index] = executeServer(ctx, server, prepared, hostKeys, definition.FailurePolicy, localOutputRoot)
		}()
	}
	group.Wait()

	execution := Execution{}
	for index, server := range servers {
		execution.Results = append(execution.Results, serverExecutions[index].results...)
		execution.Servers = append(execution.Servers, ServerResult{Address: server.Address, Success: serverExecutions[index].success})
	}
	return execution, nil
}

func executeServer(ctx context.Context, server remote.Server, steps []preparedStep, hostKeys ssh.HostKeyCallback, failurePolicy string, localOutputRoot string) serverExecution {
	result := serverExecution{success: true}
	halted := false
	for _, step := range steps {
		if halted {
			result.results = append(result.results, Result{
				Server: server.Address, StepID: step.definition.ID,
				FeatureID: step.definition.Feature, Status: StatusSkipped,
			})
			continue
		}
		for invocationIndex, invocation := range step.invocations {
			remoteResult := remote.Execute(ctx, remote.Request{
				Servers: []remote.Server{server}, Script: step.script, ScriptArgs: invocation.args,
				Artifacts: invocation.artifacts,
				Timeout:   step.timeout, HostKeys: hostKeys,
			})[0]
			status := StatusDone
			if remoteResult.Err != nil {
				status = StatusFailed
				result.success = false
				halted = failurePolicy != "continue" || ctx.Err() != nil
			}
			result.results = append(result.results, Result{
				Server: remoteResult.Address, StepID: step.definition.ID,
				FeatureID: step.definition.Feature, Invocation: invocationIndex + 1,
				Status: status, Output: remoteResult.Output, Duration: remoteResult.Duration,
				Err: remoteResult.Err,
			})
			if halted {
				break
			}
		}
	}
	if localOutputRoot != "" {
		result.results = append(result.results, fetchStepOutputs(ctx, server, steps, hostKeys, localOutputRoot)...)
	}
	return result
}

// fetchStepOutputs fetches every step's output_dir from the server after the
// run and extracts the tar.gz payload under localOutputRoot, so report files
// (comparison.csv, annotated workbooks, ...) land on the machine the tool
// runs on. Directories that do not exist remotely are skipped silently; a
// failed fetch is reported as a failed result row and does not change the
// run outcome.
func fetchStepOutputs(ctx context.Context, server remote.Server, steps []preparedStep, hostKeys ssh.HostKeyCallback, localOutputRoot string) []Result {
	var results []Result
	seen := make(map[string]bool)
	for _, step := range steps {
		for _, invocation := range step.invocations {
			directory, found := invocationOutputDir(invocation.args)
			if !found || seen[directory] {
				continue
			}
			seen[directory] = true
			started := time.Now()
			payload, err := remote.FetchDirectory(ctx, server, directory, 2*time.Minute, hostKeys)
			if err != nil {
				results = append(results, Result{
					Server: server.Address, StepID: step.definition.ID, FeatureID: step.definition.Feature,
					Status: StatusFailed, Duration: time.Since(started),
					Err: fmt.Errorf("fetch output %q: %w", directory, err),
				})
				continue
			}
			if len(payload) == 0 {
				continue
			}
			target := filepath.Join(localOutputRoot, filepath.FromSlash(directory))
			if err := extractTarGz(payload, target); err != nil {
				results = append(results, Result{
					Server: server.Address, StepID: step.definition.ID, FeatureID: step.definition.Feature,
					Status: StatusFailed, Duration: time.Since(started),
					Err: fmt.Errorf("extract output %q: %w", directory, err),
				})
				continue
			}
			results = append(results, Result{
				Server: server.Address, StepID: "output-fetch", FeatureID: step.definition.Feature,
				Status: StatusDone, Duration: time.Since(started), Output: "[INFO] reports fetched to " + target,
			})
		}
	}
	return results
}

// extractTarGz unpacks a tar.gz byte stream into target. Entries outside the
// archive root (".." or absolute paths) are rejected.
func extractTarGz(payload []byte, target string) error {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(reader)
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(header.Name)
		if !filepath.IsAbs(name) && !strings.HasPrefix(name, "..") {
			destination := filepath.Join(cleanTarget, name)
			switch header.Typeflag {
			case tar.TypeDir:
				if err := os.MkdirAll(destination, os.FileMode(header.Mode|0o700)); err != nil {
					return err
				}
			case tar.TypeReg:
				if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
					return err
				}
				file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode|0o600))
				if err != nil {
					return err
				}
				if _, err := io.Copy(file, tr); err != nil {
					file.Close()
					return err
				}
				file.Close()
			}
		}
	}
}

func (r *Registry) executeLocal(ctx context.Context, definition Definition, config Config, liveOutput io.Writer) (Execution, error) {
	execution := Execution{Servers: []ServerResult{{Address: "local", Success: true}}}
	halted := false
	for _, step := range definition.Steps {
		feature, _ := r.features.Get(step.Feature)
		if halted {
			execution.Results = append(execution.Results, Result{
				Server: "local", StepID: step.ID, FeatureID: step.Feature, Status: StatusSkipped,
			})
			continue
		}
		invocations, err := deriveInvocations(step, config)
		if err != nil {
			return Execution{}, err
		}
		for invocationIndex, invocation := range invocations {
			started := time.Now()
			fmt.Fprintf(liveOutput, "\n==> [%s.%d] %s\n", step.ID, invocationIndex+1, step.Feature)
			environment, err := localArtifactEnvironment(config.Directory, invocation.Artifacts)
			if err != nil {
				execution.Servers[0].Success = false
				halted = definition.FailurePolicy != "continue" || ctx.Err() != nil
				execution.Results = append(execution.Results, Result{
					Server: "local", StepID: step.ID, FeatureID: step.Feature,
					Invocation: invocationIndex + 1, Status: StatusFailed,
					Duration: time.Since(started), Err: fmt.Errorf("load artifacts: %w", err),
				})
				fmt.Fprintf(liveOutput, "[FAIL] %s.%d: %v\n", step.ID, invocationIndex+1, err)
				if halted {
					break
				}
				continue
			}
			environment = append(environment, "PYTHONUNBUFFERED=1")
			runContext, cancel := context.WithTimeout(ctx, time.Duration(feature.TimeoutSeconds)*time.Second)
			arguments := append([]string{"bash", filepath.Join(feature.Directory, feature.Entrypoint)}, invocation.Args...)
			command := exec.CommandContext(runContext, "/usr/bin/env", arguments...)
			command.Env = mergeEnvironment(os.Environ(), environment)
			var output bytes.Buffer
			stream := io.MultiWriter(&output, liveOutput)
			command.Stdout = stream
			command.Stderr = stream
			runErr := command.Run()
			if runContext.Err() == context.DeadlineExceeded {
				runErr = fmt.Errorf("timed out after %s", time.Duration(feature.TimeoutSeconds)*time.Second)
			}
			cancel()

			status := StatusDone
			if runErr != nil {
				status = StatusFailed
				execution.Servers[0].Success = false
				halted = definition.FailurePolicy != "continue" || ctx.Err() != nil
			}
			execution.Results = append(execution.Results, Result{
				Server: "local", StepID: step.ID, FeatureID: step.Feature,
				Invocation: invocationIndex + 1, Status: status, Output: output.String(),
				Duration: time.Since(started), Err: runErr,
			})
			if runErr != nil {
				fmt.Fprintf(liveOutput, "[FAIL] %s.%d (%s): %v\n", step.ID, invocationIndex+1, time.Since(started).Round(time.Millisecond), runErr)
			} else {
				fmt.Fprintf(liveOutput, "[DONE] %s.%d (%s)\n", step.ID, invocationIndex+1, time.Since(started).Round(time.Millisecond))
			}
			if halted {
				break
			}
		}
	}
	return execution, nil
}

func localArtifactEnvironment(configDirectory string, references []ArtifactReference) ([]string, error) {
	environment := make([]string, 0, len(references))
	for _, reference := range references {
		source := reference.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(configDirectory, source)
		}
		if _, err := remote.LoadArtifact(reference.ID, source); err != nil {
			return nil, err
		}
		absoluteSource, err := filepath.Abs(source)
		if err != nil {
			return nil, fmt.Errorf("resolve artifact %q: %w", reference.ID, err)
		}
		name := "SYSSETUP_ARTIFACT_" + strings.ToUpper(strings.ReplaceAll(reference.ID, "-", "_"))
		environment = append(environment, name+"="+absoluteSource)
	}
	return environment, nil
}

func mergeEnvironment(base, overrides []string) []string {
	overridden := make(map[string]bool, len(overrides))
	for _, value := range overrides {
		name, _, _ := strings.Cut(value, "=")
		overridden[name] = true
	}
	merged := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		name, _, _ := strings.Cut(value, "=")
		if !overridden[name] {
			merged = append(merged, value)
		}
	}
	return append(merged, overrides...)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}