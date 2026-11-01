package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindArchiverPlatformAndFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		kind       Kind
		siblings   []string
		pathTools  []string
		want       string
	}{
		{"apple-system-ar-only", "darwin", Clang, nil, []string{"ar"}, "path/ar"},
		{"apple-prefers-native-ar", "darwin", Clang, []string{"llvm-ar"}, []string{"ar"}, "path/ar"},
		{"apple-sibling-ar", "darwin", Clang, []string{"ar"}, []string{"ar"}, "compiler/ar"},
		{"apple-llvm-fallback", "darwin", Clang, []string{"llvm-ar"}, nil, "compiler/llvm-ar"},
		{"linux-llvm-preferred", "linux", Clang, []string{"ar"}, []string{"llvm-ar"}, "path/llvm-ar"},
		{"linux-ar-only", "linux", Clang, nil, []string{"ar"}, "path/ar"},
		{"linux-sibling-without-exe", "linux", Clang, []string{"llvm-ar"}, []string{"llvm-ar"}, "compiler/llvm-ar"},
		{"windows-llvm-sibling", "windows", Clang, []string{"llvm-ar"}, nil, "compiler/llvm-ar"},
		{"gcc-ar-only", "linux", GCC, nil, []string{"llvm-ar", "ar"}, "path/ar"},
		{"gcc-does-not-use-llvm-implicitly", "linux", GCC, nil, []string{"llvm-ar"}, "ar"},
		{"apple-missing-tools", "darwin", Clang, nil, nil, "ar"},
		{"linux-missing-tools", "linux", Clang, nil, nil, "llvm-ar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			compilerDir := filepath.Join(root, "compiler space")
			pathDir := filepath.Join(root, "path space")
			for _, dir := range []string{compilerDir, pathDir} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", pathDir)
			t.Setenv("PATHEXT", ".EXE")
			for dir, names := range map[string][]string{compilerDir: tc.siblings, pathDir: tc.pathTools} {
				for _, name := range names {
					if err := os.WriteFile(filepath.Join(dir, executableName(name)), []byte("stub"), 0755); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := tc.want
			if len(want) > 5 && want[:5] == "path/" {
				want = filepath.Join(pathDir, executableName(want[5:]))
			} else if len(want) > 9 && want[:9] == "compiler/" {
				want = filepath.Join(compilerDir, executableName(want[9:]))
			}
			compiler := filepath.Join(compilerDir, executableName("clang++"))
			if got := findArchiverForHost(tc.kind, compiler, tc.host); got != want {
				t.Fatalf("archiver = %q, want %q", got, want)
			}
		})
	}
}

func TestFindArchiverRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	t.Setenv("PATHEXT", ".EXE")
	if err := os.Mkdir(filepath.Join(root, executableName("ar")), 0755); err != nil {
		t.Fatal(err)
	}
	if got := findArchiverForHost(Clang, filepath.Join(root, "clang++"), "darwin"); got != "ar" {
		t.Fatalf("directory accepted as executable: %q", got)
	}
}
