package toolchain

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeSTLHeader(t *testing.T, root string, required int) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	header := "// fixture\n#ifndef _ALLOW_COMPILER_AND_STL_VERSION_MISMATCH\n#if defined(__clang__)\n#if __clang_major__ < " + strconv.Itoa(required) + "\n_EMIT_STL_ERROR(STL1000, \"old clang\");\n#endif\n#endif\n#endif\n"
	if err := os.WriteFile(filepath.Join(root, "yvals_core.h"), []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestClangMSVCSTLCompatibilityFlag(t *testing.T) {
	include := writeSTLHeader(t, filepath.Join(t.TempDir(), "include"), 19)
	env := map[string]string{"INCLUDE": include}
	old := Toolchain{Kind: Clang, Target: "x86_64-pc-windows-msvc", Version: "18.1.8", Env: env}
	if flag := ClangMSVCSTLCompatibilityFlag(old); flag != "-D"+CompilerSTLCompatibilityDefine {
		t.Fatalf("old clang flag = %q", flag)
	}
	current := old
	current.Version = "19.1.0"
	if flag := ClangMSVCSTLCompatibilityFlag(current); flag != "" {
		t.Fatalf("current clang should need no flag, got %q", flag)
	}
	gnu := old
	gnu.Target = "x86_64-pc-linux-gnu"
	if flag := ClangMSVCSTLCompatibilityFlag(gnu); flag != "" {
		t.Fatalf("non-MSVC target should need no flag, got %q", flag)
	}
	if flag := ClangMSVCSTLCompatibilityFlag(Toolchain{Kind: MSVC, Env: env, Version: "19.44"}); flag != "" {
		t.Fatalf("MSVC should need no flag, got %q", flag)
	}
	if flag := ClangMSVCSTLCompatibilityFlag(Toolchain{Kind: Clang, Target: old.Target, Version: "18.1.8"}); flag != "" {
		t.Fatalf("missing INCLUDE should need no flag, got %q", flag)
	}
}
