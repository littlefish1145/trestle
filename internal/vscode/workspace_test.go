package vscode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
)

func workspaceFixture(t *testing.T) (*config.WorkspaceContext, string) {
	t.Helper()
	root := t.TempDir()
	write := func(name, data string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(config.DefaultFileName, `
schema_version = 7
[project]
name = "ws"
[workspace]
members = ["core", "app"]
`)
	write(filepath.Join("core", config.DefaultFileName), `
schema_version = 7
[project]
name = "core"
[targets.util]
type = "static"
sources = ["src/util.cpp"]
`)
	write(filepath.Join("app", config.DefaultFileName), `
schema_version = 7
[project]
name = "app"
[targets.app]
type = "executable"
sources = ["src/main.cpp"]
dependencies = [{ target = "core:util" }]
`)
	wc, err := config.ParseWorkspace(filepath.Join(root, config.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	return wc, root
}

func TestFromWorkspaceQualifiesTargetsAndRunDirectories(t *testing.T) {
	wc, root := workspaceFixture(t)
	project := FromWorkspace(root, wc)
	if len(project.Members) != 2 || project.Members[0] != "core" || project.Members[1] != "app" {
		t.Fatalf("members = %#v", project.Members)
	}
	names := make([]string, 0, len(project.Targets))
	for _, target := range project.Targets {
		names = append(names, target.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "core:util") || !strings.Contains(joined, "app:app") {
		t.Fatalf("qualified targets = %s", joined)
	}
	document := Generate(project)
	labels := make([]string, 0, len(document.Tasks))
	for _, task := range document.Tasks {
		labels = append(labels, task.Label)
	}
	all := strings.Join(labels, "\n")
	for _, expected := range []string{"Trestle: Build core", "Trestle: Build app", "Trestle: Run app:app"} {
		if !strings.Contains(all, expected) {
			t.Fatalf("tasks missing %q:\n%s", expected, all)
		}
	}
	if len(document.Inputs) != 1 {
		t.Fatalf("target picker missing: %#v", document.Inputs)
	}
	options := strings.Join(document.Inputs[0].Options, ",")
	if !strings.Contains(options, "app:app") || !strings.Contains(options, "core:util") {
		t.Fatalf("picker options = %s", options)
	}
	for _, task := range document.Tasks {
		if task.Label != "Trestle: Run app:app" {
			continue
		}
		wantCommand := "${workspaceFolder}/" + filepath.ToSlash(filepath.Join("build", "debug", "app", "bin", "app"))
		if task.Command != wantCommand {
			t.Fatalf("run command = %q, want %q", task.Command, wantCommand)
		}
		if task.DependsOn != "Trestle: Build app" {
			t.Fatalf("run depends on %q", task.DependsOn)
		}
		return
	}
	t.Fatal("run task not found")
}

func TestFromConfigKeepsStandaloneTaskSet(t *testing.T) {
	cfg := config.Default("demo")
	cfg.Targets = map[string]config.Target{
		"app": {Type: "executable", Sources: []string{"src/main.cpp"}},
	}
	root := t.TempDir()
	project := FromConfig(root, cfg)
	document := Generate(project)
	for _, task := range document.Tasks {
		if strings.HasPrefix(task.Label, "Trestle: Build demo") {
			t.Fatalf("standalone project gained a member build task: %#v", task)
		}
	}
	if len(project.Members) != 0 {
		t.Fatalf("standalone project has members: %#v", project.Members)
	}
}
