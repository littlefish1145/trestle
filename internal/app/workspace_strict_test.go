package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"trestle/internal/config"
	"trestle/internal/model"
)

func TestWorkspaceBuildMembersIncludesOnlyTransitiveProviders(t *testing.T) {
	workspace := model.WorkspaceProject{Order: []string{"base", "unrelated", "mid", "app"}, Members: []model.MemberProject{{Name: "base"}, {Name: "unrelated"}, {Name: "mid", DependsOn: []string{"base"}}, {Name: "app", DependsOn: []string{"mid", "base"}}}}
	for _, c := range []struct {
		selected string
		want     []string
	}{{"app", []string{"base", "mid", "app"}}, {"mid", []string{"base", "mid"}}, {"base", []string{"base"}}, {"", workspace.Order}} {
		got := workspaceBuildMembers(workspace, c.selected)
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%q: got %v want %v", c.selected, got, c.want)
		}
		if len(got) > 0 {
			got[0] = "mutated"
		}
		if workspace.Order[0] != "base" {
			t.Fatal("selection mutated workspace order")
		}
	}
}
func TestWorkspaceCompileCommandsRemainDeterministicAcrossRepeatedPublication(t *testing.T) {
	dir := t.TempDir()
	root := writeWorkspace(t, dir, map[string]string{"core": simpleMember}, "[targets.root_tool]\ntype=\"executable\"\nsources=[\"src/main.cpp\"]\n")
	wc, err := config.ParseWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	buildRoot := filepath.Join(dir, "build")
	rootEntry := compileCommandEntry{Directory: filepath.Join(buildRoot, "debug"), File: "z.cpp", Output: "z.o", Arguments: []string{"c++", "z.cpp"}}
	memberEntry := compileCommandEntry{Directory: filepath.Join(buildRoot, "debug", "core"), File: "a.cpp", Output: "a.o", Arguments: []string{"c++", "a.cpp"}}
	for _, entry := range []compileCommandEntry{rootEntry, memberEntry} {
		if err := os.MkdirAll(entry.Directory, 0755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal([]compileCommandEntry{entry})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entry.Directory, "compile_commands.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	var first []byte
	for i := 0; i < 4; i++ {
		if err := publishWorkspaceCompileCommands(t.Context(), wc, buildRoot); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(wc.RootConfig.Build.CompileCommands)
		if err != nil {
			t.Fatal(err)
		}
		var entries []compileCommandEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 || entries[0].File != "a.cpp" || entries[1].File != "z.cpp" {
			t.Fatalf("publication %d duplicated/lost entries: %+v", i, entries)
		}
		if i == 0 {
			first = data
		} else if !reflect.DeepEqual(data, first) {
			t.Fatalf("publication %d is nondeterministic", i)
		}
	}
}
func TestWorkspaceMemberContextRejectsUnknownMemberWithoutPanic(t *testing.T) {
	wc := &config.WorkspaceContext{Members: map[string]*config.Config{}}
	if _, err := workspaceMemberContext(context.Background(), wc, "missing", "", model.WorkspaceProject{}); err == nil {
		t.Fatal("unknown member accepted")
	}
}

func FuzzSplitWorkspaceTargets(f *testing.F) {
	for _, seed := range []string{"app", "core:app", "", ":app", "core:", "a:b:c", " core:app ", "a/b:target"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, arg string) {
		member, targets, err := SplitWorkspaceTargets([]string{arg})
		if err != nil {
			return
		}
		if len(targets) != 1 || strings.TrimSpace(targets[0]) == "" || strings.Contains(targets[0], ":") {
			t.Fatalf("invalid successful parse: %q %q %v", arg, member, targets)
		}
		if strings.Contains(arg, ":") && member == "" {
			t.Fatal("lost member")
		}
	})
}
