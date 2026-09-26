package cuda

import (
	"strings"
	"testing"

	"trestle/internal/toolchain"
)

func TestRDCCompileUsesHostAndArchitectures(t *testing.T) {
	tc := Toolchain{NVCC: NVCC{Path: "nvcc"}, Host: toolchain.Toolchain{Kind: toolchain.Clang, CXX: "clang++"}, Architectures: []string{"sm_80"}, Mode: SeparateCompilation}
	_, args, err := tc.Compile(toolchain.CompileSpec{Source: "kernel.cu", Output: "kernel.o", CXXStandard: "c++20"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if !strings.Contains(text, "-ccbin clang++") || !strings.Contains(text, "-std=c++20") || !strings.Contains(text, "sm_80") || !strings.Contains(text, "-dc") {
		t.Fatalf("unexpected nvcc command: %s", text)
	}
}

func TestWSLCompileWrapsNVCC(t *testing.T) {
	tc := Toolchain{
		NVCC:          NVCC{Path: "/usr/local/cuda/bin/nvcc"},
		Host:          toolchain.Toolchain{CXX: "/usr/bin/g++", Runner: "wsl.exe", RunnerArgs: []string{"-d", "Ubuntu", "--cd", "/mnt/c/project/build/debug"}},
		Architectures: []string{"sm_90"},
	}
	exe, args, err := tc.Compile(toolchain.CompileSpec{Source: "../../../src/kernel.cu", Output: "obj/kernel.o", CXXStandard: "c++20"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if exe != "wsl.exe" || !strings.Contains(text, "--exec /usr/local/cuda/bin/nvcc") || !strings.Contains(text, "-ccbin /usr/bin/g++") {
		t.Fatalf("unexpected WSL nvcc command: %s %s", exe, text)
	}
}
