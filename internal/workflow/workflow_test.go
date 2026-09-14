package workflow

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	profileconfig "github.com/quyenhl16/script/internal/config"
	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/remote"
)

func TestLoadAndDeriveGatewayArguments(t *testing.T) {
	featuresRoot := t.TempDir()
	for _, id := range []string{"create-local-path", "create-bond-vlan", "verify-network"} {
		writeRemoteFeature(t, featuresRoot, id)
	}
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflowsRoot := t.TempDir()
	writeWorkflow(t, workflowsRoot, `{
  "apiVersion":"syssetup/workflow/v1", "id":"prepare", "name":"Prepare", "version":"1",
  "steps":[
    {"id":"paths", "feature":"create-local-path"},
    {"id":"vlan", "feature":"create-bond-vlan", "needs":["paths"]},
    {"id":"verify", "feature":"verify-network", "needs":["vlan"],
     "deriveArgs":{"step":"vlan", "prefix":"gateway=", "stripPrefix":true}}
  ]
}`)
	workflows, err := Load(workflowsRoot, features)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := workflows.Get("prepare")
	if !found || len(definition.Steps) != 3 {
		t.Fatalf("unexpected workflow: %#v", definition)
	}
	config := Config{APIVersion: configAPIVersion, Workflow: "prepare", Steps: map[string][]Invocation{
		"paths": {{Args: []string{"/data/app"}}},
		"vlan": {
			{Args: []string{"bond2.306", "gateway=10.0.36.254"}},
			{Args: []string{"bond2.307", "gateway=10.0.37.254"}},
			{Args: []string{"bond2.308", "gateway=10.0.36.254"}},
		},
	}}
	if err := workflows.ValidateConfig(definition, config); err != nil {
		t.Fatal(err)
	}
	got, err := deriveInvocations(definition.Steps[2], config)
	if err != nil {
		t.Fatal(err)
	}
	want := []Invocation{{Args: []string{"10.0.36.254", "10.0.37.254"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived invocations = %#v, want %#v", got, want)
	}
}

func TestDeriveArgumentsByIndex(t *testing.T) {
	index := 0
	step := Step{
		ID: "verify",
		DeriveArgs: &DeriveArgs{
			Step:          "vlan",
			ArgumentIndex: &index,
			RequireEach:   true,
		},
	}
	config := Config{Steps: map[string][]Invocation{
		"vlan": {
			{Args: []string{"bond2.306", "ip=10.0.36.87"}},
			{Args: []string{"bond2.307", "ip=10.0.37.87"}},
			{Args: []string{"bond2.306", "ip=10.0.36.88"}},
		},
	}}
	got, err := deriveInvocations(step, config)
	if err != nil {
		t.Fatal(err)
	}
	want := []Invocation{{Args: []string{"bond2.306", "bond2.307"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived invocations = %#v, want %#v", got, want)
	}
}

func TestLoadRejectsStepDependingOnLaterStep(t *testing.T) {
	featuresRoot := t.TempDir()
	writeRemoteFeature(t, featuresRoot, "first")
	writeRemoteFeature(t, featuresRoot, "second")
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflowsRoot := t.TempDir()
	writeWorkflow(t, workflowsRoot, `{
  "apiVersion":"syssetup/workflow/v1", "id":"bad", "name":"Bad", "version":"1",
  "steps":[
    {"id":"first", "feature":"first", "needs":["second"]},
    {"id":"second", "feature":"second"}
  ]
}`)
	if _, err := Load(workflowsRoot, features); err == nil {
		t.Fatal("expected dependency-order error")
	}
}

func TestBundledPrepareSetupDeployWorkflow(t *testing.T) {
	features, err := registry.Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := Load(filepath.Join("..", "..", "workflows"), features)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := workflows.Get("prepare-setup-deploy")
	if !found {
		t.Fatal("bundled prepare-setup-deploy workflow was not found")
	}
	config, err := LoadConfig(filepath.Join("..", "..", "workflow-configs", "prepare-setup-deploy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := workflows.ValidateConfig(definition, config); err != nil {
		t.Fatal(err)
	}
	verifyInvocations, err := deriveInvocations(definition.Steps[2], config)
	if err != nil {
		t.Fatal(err)
	}
	want := []Invocation{{Args: []string{"bond2.306"}}}
	if !reflect.DeepEqual(verifyInvocations, want) {
		t.Fatalf("verify-network arguments = %#v, want %#v", verifyInvocations, want)
	}
}

func TestBundledPostDeploymentValidationWorkflow(t *testing.T) {
	features, err := registry.Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := Load(filepath.Join("..", "..", "workflows"), features)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := workflows.Get("post-deployment-validation")
	if !found {
		t.Fatal("bundled post-deployment-validation workflow was not found")
	}
	if definition.FailurePolicy != "continue" {
		t.Fatalf("failurePolicy = %q, want continue", definition.FailurePolicy)
	}
	if definition.ExecutionMode != "local" {
		t.Fatalf("executionMode = %q, want local", definition.ExecutionMode)
	}
	wantFeatures := []string{
		"check-os",
		"k8s-service-check",
		"k8s-resource-check",
		"k8s-env-check",
		"k8s-connectivity-check",
	}
	gotFeatures := make([]string, 0, len(definition.Steps))
	for _, step := range definition.Steps {
		gotFeatures = append(gotFeatures, step.Feature)
	}
	if !reflect.DeepEqual(gotFeatures, wantFeatures) {
		t.Fatalf("workflow features = %#v, want %#v", gotFeatures, wantFeatures)
	}

	config, err := LoadConfig(filepath.Join("..", "..", "workflow-configs", "post-deployment-validation.json"))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := profileconfig.LoadProfiles(filepath.Join("..", "..", "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	config, err = workflows.ApplyProfiles(definition, config, profiles, "01HTX")
	if err != nil {
		t.Fatal(err)
	}
	if err := workflows.ValidateConfig(definition, config); err != nil {
		t.Fatal(err)
	}
	resourceInvocations := config.Steps["resource-check"]
	if len(resourceInvocations) != 1 || !contains(resourceInvocations[0].Args, "namespace=pramf01") ||
		!contains(resourceInvocations[0].Args, "input_file=pramf01-input_100K.xlsx") {
		t.Fatalf("resource-check was not built from its profile: %#v", resourceInvocations)
	}
	connectivityInvocations := config.Steps["connectivity-check"]
	if len(connectivityInvocations) != 1 || !contains(connectivityInvocations[0].Args, "ip_amf_self=") ||
		!contains(connectivityInvocations[0].Args, "curl_amf_self_urls=") ||
		!contains(connectivityInvocations[0].Args, "ip_nssf=") ||
		!contains(connectivityInvocations[0].Args, "curl_nssf_urls=") {
		t.Fatalf("optional NF parameters were not built from the connectivity profile: %#v", connectivityInvocations)
	}
}

func TestApplyProfilesOverridesWorkflowParameterArguments(t *testing.T) {
	featuresRoot := t.TempDir()
	directory := filepath.Join(featuresRoot, "check")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "apiVersion":"syssetup/v1", "id":"check", "name":"Check", "version":"1",
  "entrypoint":"run.sh", "workflowCompatible":true, "timeoutSeconds":30,
  "parameters":{
    "namespace":{"type":"string","default":"default-ns"},
    "show_pass":{"type":"boolean","default":false}
  }
}`
	if err := os.WriteFile(filepath.Join(directory, "feature.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "run.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflows := &Registry{features: features}
	definition := Definition{Steps: []Step{{ID: "check-step", Feature: "check"}}}
	config := Config{Steps: map[string][]Invocation{
		"check-step": {{Args: []string{"verify", "namespace=stale", "unrelated=keep"}}},
	}}
	profiles := []domain.Profile{{
		Name: "check", System: "system-a",
		Features: []domain.FeatureSelection{{
			ID: "check", Parameters: map[string]any{"namespace": "profile-ns", "show_pass": true},
		}},
	}}

	got, err := workflows.ApplyProfiles(definition, config, profiles, "system-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"verify", "unrelated=keep", "namespace=profile-ns", "show_pass=true"}
	if !reflect.DeepEqual(got.Steps["check-step"][0].Args, want) {
		t.Fatalf("profile arguments = %#v, want %#v", got.Steps["check-step"][0].Args, want)
	}
}

func TestLocalArtifactEnvironmentUsesAbsoluteSourcePath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "baseline.xlsx")
	if err := os.WriteFile(path, []byte("test workbook"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := localArtifactEnvironment(directory, []ArtifactReference{{ID: "input-file", Source: "baseline.xlsx"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SYSSETUP_ARTIFACT_INPUT_FILE=" + path}
	if !reflect.DeepEqual(environment, want) {
		t.Fatalf("artifact environment = %#v, want %#v", environment, want)
	}
}

func TestLocalWorkflowNeedsNoServerAndContinuesAfterArtifactFailure(t *testing.T) {
	featuresRoot := t.TempDir()
	writeFeature(t, featuresRoot, "first-feature", false, true)
	writeFeature(t, featuresRoot, "second-feature", false, true)
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflows := &Registry{features: features}
	definition := Definition{
		ExecutionMode: "local",
		FailurePolicy: "continue",
		Steps: []Step{
			{ID: "first", Feature: "first-feature"},
			{ID: "second", Feature: "second-feature", Needs: []string{"first"}},
		},
	}
	missingArtifact := []ArtifactReference{{ID: "input-file", Source: "missing.xlsx"}}
	config := Config{Directory: t.TempDir(), Steps: map[string][]Invocation{
		"first":  {{Args: []string{"verify"}, Artifacts: missingArtifact}},
		"second": {{Args: []string{"verify"}, Artifacts: missingArtifact}},
	}}

	var liveOutput bytes.Buffer
	execution, err := workflows.executeLocal(context.Background(), definition, config, &liveOutput)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.Servers) != 1 || execution.Servers[0].Address != "local" || execution.Servers[0].Success {
		t.Fatalf("unexpected local execution target: %#v", execution.Servers)
	}
	if len(execution.Results) != 2 {
		t.Fatalf("result count = %d, want 2", len(execution.Results))
	}
	for index, result := range execution.Results {
		if result.Status != StatusFailed || result.Err == nil {
			t.Fatalf("result %d = %#v, want failed artifact result", index, result)
		}
	}
	if output := liveOutput.String(); !strings.Contains(output, "==> [first.1]") || !strings.Contains(output, "[FAIL] second.1") {
		t.Fatalf("live output did not include step progress: %q", output)
	}
}

func TestExecuteServerContinuesAfterFailure(t *testing.T) {
	steps := []preparedStep{
		{
			definition:  Step{ID: "first", Feature: "first-feature"},
			script:      []byte("#!/usr/bin/env bash\nexit 0\n"),
			invocations: []preparedInvocation{{}},
			timeout:     time.Second,
		},
		{
			definition:  Step{ID: "second", Feature: "second-feature"},
			script:      []byte("#!/usr/bin/env bash\nexit 0\n"),
			invocations: []preparedInvocation{{}},
			timeout:     time.Second,
		},
	}

	result := executeServer(context.Background(), remote.Server{}, steps, nil, "continue")
	if result.success {
		t.Fatal("server execution with failed invocations must not be successful")
	}
	if len(result.results) != 2 {
		t.Fatalf("result count = %d, want 2", len(result.results))
	}
	for index, item := range result.results {
		if item.Status != StatusFailed {
			t.Fatalf("result %d status = %q, want failed", index, item.Status)
		}
	}
}

func TestExecuteServerStillStopsWithDefaultPolicy(t *testing.T) {
	steps := []preparedStep{
		{
			definition:  Step{ID: "first", Feature: "first-feature"},
			script:      []byte("#!/usr/bin/env bash\nexit 0\n"),
			invocations: []preparedInvocation{{}},
			timeout:     time.Second,
		},
		{
			definition:  Step{ID: "second", Feature: "second-feature"},
			script:      []byte("#!/usr/bin/env bash\nexit 0\n"),
			invocations: []preparedInvocation{{}},
			timeout:     time.Second,
		},
	}

	result := executeServer(context.Background(), remote.Server{}, steps, nil, "")
	if len(result.results) != 2 {
		t.Fatalf("result count = %d, want 2", len(result.results))
	}
	if result.results[0].Status != StatusFailed || result.results[1].Status != StatusSkipped {
		t.Fatalf("statuses = [%q, %q], want [failed, skipped]", result.results[0].Status, result.results[1].Status)
	}
}

func TestLoadAllowsOnlyOptedInLocalFeature(t *testing.T) {
	featuresRoot := t.TempDir()
	writeFeature(t, featuresRoot, "compatible", false, true)
	writeFeature(t, featuresRoot, "local-only", false, false)
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}

	compatibleRoot := t.TempDir()
	writeWorkflow(t, compatibleRoot, `{
  "apiVersion":"syssetup/workflow/v1", "id":"compatible-workflow", "name":"Compatible", "version":"1",
  "steps":[{"id":"check", "feature":"compatible"}]
}`)
	if _, err := Load(compatibleRoot, features); err != nil {
		t.Fatalf("workflow-compatible local feature was rejected: %v", err)
	}

	localRoot := t.TempDir()
	writeWorkflow(t, localRoot, `{
  "apiVersion":"syssetup/workflow/v1", "id":"local-workflow", "name":"Local", "version":"1",
  "steps":[{"id":"check", "feature":"local-only"}]
}`)
	if _, err := Load(localRoot, features); err == nil {
		t.Fatal("expected local feature without workflow opt-in to be rejected")
	}
}

func TestLoadRejectsRemoteOnlyFeatureInLocalWorkflow(t *testing.T) {
	featuresRoot := t.TempDir()
	writeFeature(t, featuresRoot, "remote-only", true, false)
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflowsRoot := t.TempDir()
	writeWorkflow(t, workflowsRoot, `{
  "apiVersion":"syssetup/workflow/v1", "id":"local-workflow", "name":"Local", "version":"1",
  "executionMode":"local", "steps":[{"id":"check", "feature":"remote-only"}]
}`)
	if _, err := Load(workflowsRoot, features); err == nil {
		t.Fatal("expected remote-only feature in local workflow to be rejected")
	}
}

func writeRemoteFeature(t *testing.T, root, id string) {
	writeFeature(t, root, id, true, false)
}

func writeFeature(t *testing.T, root, id string, remoteOnly, workflowCompatible bool) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(
		`{"apiVersion":"syssetup/v1","id":%q,"name":%q,"version":"1","entrypoint":"run.sh","remoteOnly":%t,"workflowCompatible":%t,"timeoutSeconds":30}`,
		id, id, remoteOnly, workflowCompatible,
	)
	if err := os.WriteFile(filepath.Join(directory, "feature.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "run.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeWorkflow(t *testing.T, root, manifest string) {
	t.Helper()
	directory := filepath.Join(root, "workflow")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
