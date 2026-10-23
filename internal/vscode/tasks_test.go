package vscode

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trestle/internal/config"
)

func TestFromConfigReducesProject(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Build: config.Build{
			Profile:        "release",
			BuildDir:       filepath.Join(root, "out"),
			DefaultTargets: []string{"tool", "app"},
		},
		Targets: map[string]config.Target{
			"library": {Type: "static", OutputName: "core"},
			"app":     {Type: "executable"},
		},
	}
	project := FromConfig(root, cfg)
	if project.BinDir != "out/release/bin" {
		t.Fatalf("BinDir = %q, want out/release/bin", project.BinDir)
	}
	if !project.Native {
		t.Fatal("a native toolchain must keep the Windows override")
	}
	if project.DefaultTarget != "app" {
		t.Fatalf("DefaultTarget = %q, want app: the configured tool target does not exist", project.DefaultTarget)
	}
	if len(project.Targets) != 2 || project.Targets[0].Name != "app" || project.Targets[1].Name != "library" {
		t.Fatalf("targets must be sorted by name: %#v", project.Targets)
	}
	if project.Targets[0].OutputName != "app" {
		t.Fatalf("an empty output name must fall back to the target name: %#v", project.Targets[0])
	}
	if !project.Targets[0].Executable {
		t.Fatal("the executable target must be marked runnable")
	}
	if project.Targets[1].OutputName != "core" || project.Targets[1].Executable {
		t.Fatalf("static libraries must keep their output name and stay non-runnable: %#v", project.Targets[1])
	}
}

func TestFromConfigResolvesRelativeBuildDirAndDefaults(t *testing.T) {
	project := FromConfig(t.TempDir(), config.Config{Build: config.Build{BuildDir: "build"}})
	want := filepath.ToSlash(filepath.Join("build", "debug", "bin"))
	if project.BinDir != want {
		t.Fatalf("BinDir = %q, want %q", project.BinDir, want)
	}
	if project.Native != true || project.DefaultTarget != "" {
		t.Fatalf("unexpected defaults: %#v", project)
	}
}

func TestFromConfigKeepsBuildDirsOutsideTheWorkspaceAddressable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	project := FromConfig(root, config.Config{Build: config.Build{BuildDir: filepath.Join(root, "..", "out")}})
	if project.BinDir != "../out/debug/bin" {
		t.Fatalf("BinDir = %q, want ../out/debug/bin", project.BinDir)
	}
}

func TestFromConfigMarksWSLBuildsAsNonNative(t *testing.T) {
	project := FromConfig(t.TempDir(), config.Config{Toolchain: config.Toolchain{Mode: "WSL"}})
	if project.Native {
		t.Fatal("WSL builds produce Linux binaries and must not get a Windows override")
	}
}

func TestFromConfigSkipsRunPathsOnAnotherVolume(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows can place the build directory on a second volume")
	}
	project := FromConfig(`C:\workspace`, config.Config{Build: config.Build{BuildDir: `D:\build`}})
	if project.BinDir != "" {
		t.Fatalf("BinDir = %q, want empty for a cross-volume build directory", project.BinDir)
	}
}

func TestFromConfigNormalizesOutputNames(t *testing.T) {
	project := FromConfig(t.TempDir(), config.Config{Targets: map[string]config.Target{
		"app": {Type: "executable", OutputName: `bin\app`},
	}})
	if len(project.Targets) != 1 || project.Targets[0].OutputName != "bin/app" {
		t.Fatalf("output names must use forward slashes: %#v", project.Targets)
	}
}

func TestGenerateWritesTheFullDocument(t *testing.T) {
	document := Generate(Project{
		BinDir: "build/debug/bin",
		Targets: []Target{
			{Name: "core", OutputName: "core"},
			{Name: "app", OutputName: "app", Executable: true},
		},
		DefaultTarget: "app",
		Native:        true,
	})
	data := Encode(document)
	want := `{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "Trestle: Build",
      "type": "shell",
      "command": "trestle",
      "args": [
        "build"
      ],
      "group": {
        "kind": "build",
        "isDefault": true
      },
      "problemMatcher": [],
      "presentation": {
        "reveal": "always",
        "panel": "shared"
      },
      "options": {
        "cwd": "${workspaceFolder}"
      }
    },
    {
      "label": "Trestle: Build All",
      "type": "shell",
      "command": "trestle",
      "args": [
        "build",
        "--all"
      ],
      "problemMatcher": [],
      "presentation": {
        "reveal": "always",
        "panel": "shared"
      },
      "options": {
        "cwd": "${workspaceFolder}"
      }
    },
    {
      "label": "Trestle: Build Current Target",
      "type": "shell",
      "command": "trestle",
      "args": [
        "build",
        "${input:trestleTarget}"
      ],
      "problemMatcher": [],
      "presentation": {
        "reveal": "always",
        "panel": "shared"
      },
      "options": {
        "cwd": "${workspaceFolder}"
      }
    },
    {
      "label": "Trestle: Run app",
      "type": "process",
      "command": "${workspaceFolder}/build/debug/bin/app",
      "dependsOn": "Trestle: Build",
      "problemMatcher": [],
      "presentation": {
        "reveal": "always",
        "panel": "dedicated",
        "focus": true
      },
      "options": {
        "cwd": "${workspaceFolder}"
      },
      "windows": {
        "command": "${workspaceFolder}/build/debug/bin/app.exe"
      }
    },
    {
      "label": "Trestle: Test",
      "type": "shell",
      "command": "trestle",
      "args": [
        "test",
        "--all"
      ],
      "group": {
        "kind": "test",
        "isDefault": true
      },
      "problemMatcher": [],
      "presentation": {
        "reveal": "always",
        "panel": "shared"
      },
      "options": {
        "cwd": "${workspaceFolder}"
      }
    },
    {
      "label": "Trestle: Release",
      "type": "shell",
      "command": "trestle",
      "args": [
        "release"
      ],
      "problemMatcher": [],
      "presentation": {
        "reveal": "always",
        "panel": "shared"
      },
      "options": {
        "cwd": "${workspaceFolder}"
      }
    }
  ],
  "inputs": [
    {
      "id": "trestleTarget",
      "type": "pickString",
      "description": "Select the Trestle target to build",
      "options": [
        "app",
        "core"
      ],
      "default": "app"
    }
  ]
}
`
	if strings.TrimSpace(string(data)) != strings.TrimSpace(want) {
		t.Fatalf("generated document differs\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
}

func TestGenerateWithoutTargetsHasNoPickerOrRunTasks(t *testing.T) {
	document := Generate(Project{BinDir: "build/debug/bin", Native: true})
	if len(document.Inputs) != 0 {
		t.Fatalf("a targetless project must not prompt: %#v", document.Inputs)
	}
	labels := taskLabels(document)
	want := []string{"Trestle: Build", "Trestle: Build All", "Trestle: Test", "Trestle: Release"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
}

func TestGenerateWithoutBinDirSkipsRunTasks(t *testing.T) {
	document := Generate(Project{
		Targets: []Target{{Name: "app", OutputName: "app", Executable: true}},
		Native:  true,
	})
	for _, label := range taskLabels(document) {
		if strings.HasPrefix(label, "Trestle: Run") {
			t.Fatalf("run tasks need an addressable bin directory: %v", taskLabels(document))
		}
	}
}

func TestGenerateSkipsNonExecutableTargets(t *testing.T) {
	document := Generate(Project{
		BinDir:  "build/debug/bin",
		Targets: []Target{{Name: "core", OutputName: "core"}},
		Native:  true,
	})
	for _, label := range taskLabels(document) {
		if strings.HasPrefix(label, "Trestle: Run") {
			t.Fatalf("libraries must not get run tasks: %v", taskLabels(document))
		}
	}
	if len(document.Inputs) != 1 || document.Inputs[0].Default != "core" {
		t.Fatalf("the picker must still default to the only target: %#v", document.Inputs)
	}
}

func TestGenerateWSLRunTasksHaveNoWindowsOverride(t *testing.T) {
	document := Generate(Project{
		BinDir:  "build/debug/bin",
		Targets: []Target{{Name: "app", OutputName: "app", Executable: true}},
		Native:  false,
	})
	for _, task := range document.Tasks {
		if task.Label != "Trestle: Run app" {
			continue
		}
		if task.Windows != nil {
			t.Fatalf("WSL run tasks must not append .exe: %#v", task.Windows)
		}
		if task.Command != "${workspaceFolder}/build/debug/bin/app" {
			t.Fatalf("command = %q", task.Command)
		}
		return
	}
	t.Fatalf("missing run task: %v", taskLabels(document))
}

func TestGenerateSortsTargetsAndFallsBackToFirstDefault(t *testing.T) {
	document := Generate(Project{
		BinDir:  "build/debug/bin",
		Targets: []Target{{Name: "zeta", OutputName: "zeta"}, {Name: "alpha", OutputName: "alpha"}},
		Native:  true,
	})
	if len(document.Inputs) != 1 {
		t.Fatalf("missing picker: %#v", document.Inputs)
	}
	input := document.Inputs[0]
	if strings.Join(input.Options, ",") != "alpha,zeta" || input.Default != "alpha" {
		t.Fatalf("picker = %#v, want sorted options defaulting to alpha", input)
	}
}

// taskLabels extracts the task labels in document order.
func taskLabels(document Document) []string {
	labels := make([]string, 0, len(document.Tasks))
	for _, task := range document.Tasks {
		labels = append(labels, task.Label)
	}
	return labels
}
