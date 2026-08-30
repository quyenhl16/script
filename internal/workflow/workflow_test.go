package workflow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/quyenhl16/script/internal/registry"
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
	want := []Invocation{{Args: []string{"10.0.36.254"}}}
	if !reflect.DeepEqual(verifyInvocations, want) {
		t.Fatalf("verify-network arguments = %#v, want %#v", verifyInvocations, want)
	}
}

func TestBundledVerifySystemXMLWorkflowLoadsArtifact(t *testing.T) {
	features, err := registry.Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := Load(filepath.Join("..", "..", "workflows"), features)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := workflows.Get("verify-system-xml")
	if !found {
		t.Fatal("bundled verify-system-xml workflow was not found")
	}
	config, err := LoadConfig(filepath.Join("..", "..", "workflow-configs", "verify-system-xml.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := workflows.ValidateConfig(definition, config); err != nil {
		t.Fatal(err)
	}
	if _, err := workflows.Execute(context.Background(), definition, config, nil, nil); err != nil {
		t.Fatalf("workflow could not load its local rules artifact: %v", err)
	}
}

func writeRemoteFeature(t *testing.T, root, id string) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"apiVersion":"syssetup/v1","id":"` + id + `","name":"` + id + `","version":"1","entrypoint":"run.sh","remoteOnly":true,"timeoutSeconds":30}`
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
