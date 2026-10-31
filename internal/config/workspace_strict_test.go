package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestMemberDirStrictPathMatrix(t *testing.T) {
	root := t.TempDir()
	for _, entry := range []string{"", " ", ".", "./", "..", "../escape", "nested/../../escape", filepath.Join(root, "absolute")} {
		t.Run(entry, func(t *testing.T) {
			if _, _, err := MemberDir(root, entry); err == nil || !strings.Contains(err.Error(), "E_WORKSPACE_INVALID") {
				t.Fatalf("unsafe member %q accepted: %v", entry, err)
			}
		})
	}
	for _, entry := range []string{"core", "./core", "nested/../core", " core "} {
		name, dir, err := MemberDir(root, entry)
		if err != nil || name != "core" || dir != filepath.Join(root, "core") {
			t.Fatalf("normalization %q: %q %q %v", entry, name, dir, err)
		}
	}
	name, _, err := MemberDir(root, "nested/member")
	if err != nil || name != "nested/member" {
		t.Fatalf("portable nested name: %q %v", name, err)
	}
}
func TestWorkspaceRejectsAliasedMemberDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, members := range []string{`["core", "./core"]`, `["core", "nested/../core"]`} {
		root := writeToml(t, dir, DefaultFileName, "schema_version=7\n[project]\nname=\"ws\"\n[workspace]\nmembers="+members+"\n")
		writeToml(t, dir, "core/"+DefaultFileName, memberToml)
		if _, err := ParseWorkspace(root); err == nil || !strings.Contains(err.Error(), "listed twice") {
			t.Fatalf("aliased members accepted: %v", err)
		}
	}
	if runtime.GOOS == "windows" {
		root := writeToml(t, dir, DefaultFileName, "schema_version=7\n[project]\nname=\"ws\"\n[workspace]\nmembers=[\"core\",\"CORE\"]\n")
		if _, err := ParseWorkspace(root); err == nil {
			t.Fatal("Windows case alias accepted")
		}
	}
}
func TestWorkspaceRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "member")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, _, err := MemberDir(root, "member"); err == nil {
		t.Fatal("symlink escaped workspace")
	}
	if _, _, err := MemberDir(root, "member/not-created-yet"); err == nil {
		t.Fatal("missing leaf bypassed symlink confinement")
	}
}
func TestWorkspaceMemberNamesReturnIndependentCopy(t *testing.T) {
	wc := &WorkspaceContext{MemberOrder: []string{"core", "cli"}}
	got := wc.MemberNames()
	got[0] = "changed"
	if !reflect.DeepEqual(wc.MemberNames(), []string{"core", "cli"}) {
		t.Fatal("MemberNames leaked internal storage")
	}
	var empty *WorkspaceContext
	if empty.MemberNames() != nil || empty.Member("x") != nil {
		t.Fatal("nil workspace accessors failed")
	}
}

func FuzzMemberDirCannotEscapeRoot(f *testing.F) {
	root := f.TempDir()
	for _, seed := range []string{"core", "../outside", "nested/../../outside", "nested/member", ".", "", string([]byte{0})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, member string) {
		name, dir, err := MemberDir(root, member)
		if err != nil {
			return
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || name == "" || name == "." {
			t.Fatalf("unsafe successful resolution %q => %q %q", member, name, dir)
		}
	})
}

func TestCUDAConfigurationRejectsInvalidModeAndArchitecture(t *testing.T) {
	for _, mode := range []string{"typo", "RDC", "seperated"} {
		cfg := Default("cuda")
		cfg.Toolchain.CUDAMode = mode
		if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "cuda_mode") {
			t.Fatalf("invalid mode %q: %v", mode, err)
		}
	}
	for _, arch := range []string{"", "80", "compute_80", "sm_80,sm_90", "sm_80\n"} {
		cfg := Default("cuda")
		cfg.Toolchain.CUDAArchitectures = []string{arch}
		if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "cuda_architectures") {
			t.Fatalf("invalid architecture %q: %v", arch, err)
		}
	}
}
