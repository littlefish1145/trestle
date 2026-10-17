package tui

import (
	"slices"
	"strings"
	"testing"

	"trestle/internal/toolchain"
)

// The legacy wizard keeps every tab value independent and defaults the profile.
func TestWizardStateAndArchiver(t *testing.T) {
	model := New("demo", "", "", []toolchain.Toolchain{{Archiver: "lib.exe"}, {Archiver: "llvm-lib"}})
	if model.State().Profile != "debug" {
		t.Fatalf("default profile = %q", model.State().Profile)
	}
	if model.State().Archiver != "lib.exe" {
		t.Fatalf("archiver = %q", model.State().Archiver)
	}
	if archiver := firstArchiver(nil); archiver != "" {
		t.Fatalf("empty candidates archiver = %q", archiver)
	}
	if archiver := New("demo", "release", "cc", nil).State().Archiver; archiver != "" {
		t.Fatalf("no-candidates archiver = %q", archiver)
	}
}

// Every action moves one step and InputAction commits the value per tab.
func TestWizardUpdatePerTab(t *testing.T) {
	model := New("demo", "release", "cc", nil)

	const total = 4
	seen := []Tab{}
	for i := 0; i < total; i++ {
		seen = append(seen, model.State().Tab)
		model.Update(NextAction, "")
	}
	if !slices.Equal(seen, []Tab{ProjectTab, ToolchainTab, BuildTab, PackageTab}) {
		t.Fatalf("tab order = %v", seen)
	}
	model.Update(NextAction, "")
	if model.State().Tab != ToolchainTab {
		t.Fatalf("next must advance, at %v", model.State().Tab)
	}
	model.Update(PreviousAction, "")
	if model.State().Tab != ProjectTab {
		t.Fatalf("previous must step back, at %v", model.State().Tab)
	}

	model.Update(ToggleEditAction, "")
	if !model.State().Editing {
		t.Fatal("toggle did not enter edit mode")
	}
	model.Update(ConfirmAction, "")
	if model.State().Editing || !model.State().Done {
		t.Fatalf("confirm left editing=%v done=%v", model.State().Editing, model.State().Done)
	}

	for tab, want := range map[Tab]string{ProjectTab: "renamed", ToolchainTab: "clang", BuildTab: "size", PackageTab: "x64-windows"} {
		editor := New("demo", "release", "", nil)
		editor.state.Tab = tab
		editor.state.Editing = true
		editor.Update(InputAction, want)
		if editor.State().Editing {
			t.Fatalf("input on tab %v kept editing", tab)
		}
		got := editor.State().ProjectName
		switch tab {
		case ToolchainTab:
			got = editor.State().Compiler
		case BuildTab:
			got = editor.State().Profile
		case PackageTab:
			got = editor.State().Triplet
		}
		if got != want {
			t.Fatalf("tab %v kept %q", tab, got)
		}
	}
}

// Render marks the active tab, exposes edit prompts and forwards errors.
func TestWizardRenderMarksTabsAndEditing(t *testing.T) {
	model := New("demo", "release", "clang", nil)
	view := model.Render()
	if !strings.Contains(view, "Trestle configuration") {
		t.Fatal("render lost the title")
	}
	if !strings.Contains(view, "> Project    demo") || !strings.Contains(view, "  Toolchain ") {
		t.Fatalf("active tab marker wrong:\n%s", view)
	}
	model.Update(ToggleEditAction, "")
	if !strings.Contains(model.Render(), "> edit project name") {
		t.Fatalf("edit prompt missing:\n%s", model.Render())
	}
	model.state.Error = "broken"
	if !strings.Contains(model.Render(), "error: broken") {
		t.Fatal("error line missing")
	}
	if label := model.currentLabel(); label != "project name" {
		t.Fatalf("project label = %q", label)
	}
	editor := New("demo", "", "", nil)
	editor.state.Tab = PackageTab
	if label := editor.currentLabel(); label != "triplet" {
		t.Fatalf("triplet label = %q", label)
	}
	editor.state.Tab = BuildTab
	if label := editor.currentLabel(); label != "profile" {
		t.Fatalf("build label = %q", label)
	}
}
