package cuda

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"trestle/internal/toolchain"
)

// A real child process verifies the environment and exit status contracts,
// without depending on a CUDA installation or shell-specific scripts.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TRESTLE_CUDA_HELPER"); mode != "" {
		if mode == "wait" {
			time.Sleep(time.Minute)
			os.Exit(0)
		}
		if mode == "fail" {
			fmt.Fprintln(os.Stderr, "deliberate nvcc failure")
			os.Exit(23)
		}
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			fmt.Println("nvcc test toolkit")
			os.Exit(0)
		}
		if os.Getenv("TRESTLE_HOST_SENTINEL") != "host environment reached nvcc" {
			fmt.Fprintln(os.Stderr, "missing host environment")
			os.Exit(24)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func strictToolchain() Toolchain {
	return Toolchain{NVCC: NVCC{Path: "nvcc"}, Host: toolchain.Toolchain{Kind: toolchain.MSVC, CXX: "C:/host tools/cl.exe"}, Architectures: []string{"sm_80", "sm_90a"}}
}

func TestCompileExactArguments(t *testing.T) {
	for _, mode := range []CompileMode{WholeProgram, SeparateCompilation} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			tc := strictToolchain()
			tc.Mode = mode
			spec := toolchain.CompileSpec{Source: "source space/kernel.cu", Output: "obj space/kernel.obj", CXXStandard: "c++20", Includes: []string{"include space", "other"}, Defines: []string{"VALUE=2", "TEXT=hello world"}, Depfile: "obj space/kernel.obj.d", Options: []string{"/MDd", "-fPIC", "--use_fast_math"}}
			exe, args, err := tc.Compile(spec)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"-ccbin", tc.Host.CXX, "-x", "cu", "-std=c++20", "-Iinclude space", "-Iother", "-DVALUE=2", "-DTEXT=hello world", "-gencode=arch=compute_80,code=sm_80", "-gencode=arch=compute_90a,code=sm_90a"}
			if mode == WholeProgram {
				want = append(want, "-c")
			} else {
				want = append(want, "-dc")
			}
			want = append(want, "-MD", "-MF", spec.Depfile, spec.Source, "-o", spec.Output, "-Xcompiler", "/MDd", "-Xcompiler", "-fPIC", "--use_fast_math")
			if exe != "nvcc" || !reflect.DeepEqual(args, want) {
				t.Fatalf("got %q %#v; want nvcc %#v", exe, args, want)
			}
		})
	}
}

func TestDeviceLinkPreservesArchitecturesAndObjects(t *testing.T) {
	tc := strictToolchain()
	tc.Mode = SeparateCompilation
	objects := []string{"a space.obj", "b.obj", "lib kernels.lib"}
	before := append([]string(nil), objects...)
	exe, args, err := tc.DeviceLink(objects, "device.obj")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-ccbin", tc.Host.CXX, "-dlink", "-gencode=arch=compute_80,code=sm_80", "-gencode=arch=compute_90a,code=sm_90a", "a space.obj", "b.obj", "lib kernels.lib", "-o", "device.obj"}
	if exe != "nvcc" || !reflect.DeepEqual(args, want) || !reflect.DeepEqual(objects, before) {
		t.Fatalf("device link mutated inputs or arguments: %s %#v", exe, args)
	}
}

func TestInvalidToolchainRejectedBeforeExecution(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Toolchain)
		code   string
	}{
		{"nvcc", func(tc *Toolchain) { tc.NVCC.Path = "" }, "E_CUDA_NVCC"},
		{"host", func(tc *Toolchain) { tc.Host.CXX = "" }, "E_CUDA_HOST"},
		{"mode", func(tc *Toolchain) { tc.Mode = CompileMode(255) }, "E_CUDA_MODE"},
	}
	for _, arch := range []string{"", "80", "compute_80", "sm_", "sm_80;echo", "sm_80\n", "sm_8", "sm_1234", "sm_90z"} {
		cases = append(cases, struct {
			name   string
			change func(*Toolchain)
			code   string
		}{"arch/" + arch, func(tc *Toolchain) { tc.Architectures = []string{arch} }, "E_CUDA_ARCHITECTURE"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tc := strictToolchain()
			c.change(&tc)
			_, _, err := tc.Compile(toolchain.CompileSpec{})
			if err == nil || !strings.Contains(err.Error(), c.code) {
				t.Fatalf("compile: %v", err)
			}
			_, _, err = tc.DeviceLink(nil, "out")
			if err == nil || !strings.Contains(err.Error(), c.code) {
				t.Fatalf("device link: %v", err)
			}
		})
	}
}

func helperToolkit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "nvcc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDetectAndProbeChildProcessContracts(t *testing.T) {
	root := helperToolkit(t)
	t.Setenv("CUDA_PATH", root)
	t.Setenv("CUDA_HOME", t.TempDir())
	t.Setenv("TRESTLE_CUDA_HELPER", "ok")
	host := toolchain.Toolchain{CXX: "host", Env: map[string]string{"TRESTLE_HOST_SENTINEL": "host environment reached nvcc"}}
	tc, err := DetectWithHost(context.Background(), "", host)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Toolkit.Root != root || tc.NVCC.Version != "nvcc test toolkit" || !reflect.DeepEqual(tc.Host, host) {
		t.Fatalf("detection lost configuration: %+v", tc)
	}
	if err := ProbeHost(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRESTLE_CUDA_HELPER", "fail")
	if _, err := DetectWithHost(context.Background(), root, host); err == nil || !strings.Contains(err.Error(), "deliberate nvcc failure") {
		t.Fatalf("ignored version failure: %v", err)
	}
	if err := ProbeHost(context.Background(), tc); err == nil || !strings.Contains(err.Error(), "deliberate nvcc failure") {
		t.Fatalf("ignored probe failure: %v", err)
	}
	t.Setenv("TRESTLE_CUDA_HELPER", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := ProbeHost(ctx, tc); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe lost cancellation: %v", err)
	}
}

func TestToolkitRootPrecedence(t *testing.T) {
	t.Setenv("CUDA_PATH", " path root ")
	t.Setenv("CUDA_HOME", "home root")
	if got := toolkitRoot("explicit"); got != "explicit" {
		t.Fatal(got)
	}
	if got := toolkitRoot(""); got != "path root" {
		t.Fatal(got)
	}
	t.Setenv("CUDA_PATH", "  ")
	if got := toolkitRoot(""); got != "home root" {
		t.Fatal(got)
	}
	t.Setenv("CUDA_HOME", "")
	if _, err := DetectWithHost(context.Background(), "", toolchain.Toolchain{}); err == nil || !strings.Contains(err.Error(), "E_CUDA_NOT_FOUND") {
		t.Fatal(err)
	}
}

func TestRunnerArgumentsPreserveDistributionAndDoNotMutate(t *testing.T) {
	args := []string{"-d", "Ubuntu", "--cd", "/project space", "--user", "builder"}
	before := append([]string(nil), args...)
	if got := withoutRunnerDirectory(args); !reflect.DeepEqual(got, []string{"-d", "Ubuntu", "--user", "builder"}) {
		t.Fatalf("runner arguments: %v", got)
	}
	if !reflect.DeepEqual(args, before) || runnerDistribution(args) != "Ubuntu" || runnerDistribution([]string{"-d"}) != "" {
		t.Fatal("runner parsing mutated or misread arguments")
	}
	source, output, err := probePaths(t.Context(), toolchain.Toolchain{}, "source space.cu", "out.o")
	if err != nil || source != "source space.cu" || output != "out.o" {
		t.Fatalf("native probe paths: %q %q %v", source, output, err)
	}
	if _, err := DetectWSL(t.Context(), "Ubuntu", "", toolchain.Toolchain{}); err == nil || !strings.Contains(err.Error(), "E_CUDA_WSL_HOST") {
		t.Fatalf("WSL without host: %v", err)
	}
}
func TestRuntimeLibrariesMatchHostAndCompilationMode(t *testing.T) {
	for _, kind := range []toolchain.Kind{toolchain.MSVC, toolchain.GCC} {
		for _, mode := range []CompileMode{WholeProgram, SeparateCompilation} {
			tc := strictToolchain()
			tc.Host.Kind = kind
			tc.Mode = mode
			tc.Toolkit.Root = filepath.Join(t.TempDir(), "CUDA SDK")
			dir, libs := tc.RuntimeLibraries()
			want := []string{"cudart_static"}
			if mode == SeparateCompilation {
				want = append(want, "cudadevrt")
			}
			suffix := filepath.Join("lib", "x64")
			if kind != toolchain.MSVC {
				suffix = "lib64"
				want = append(want, "dl", "rt", "pthread")
			}
			if dir != filepath.Join(tc.Toolkit.Root, suffix) || !reflect.DeepEqual(libs, want) {
				t.Fatalf("runtime %s/%d: %q %v", kind, mode, dir, libs)
			}
		}
	}
	tc := strictToolchain()
	tc.Host.Kind = toolchain.GCC
	tc.Host.Runner = "wsl.exe"
	tc.Toolkit.Root = "/usr/local/cuda"
	dir, _ := tc.RuntimeLibraries()
	if dir != "/usr/local/cuda/lib64" {
		t.Fatal(dir)
	}
}
func TestFirstLineAndMissingToolkit(t *testing.T) {
	for _, c := range []struct{ input, want string }{{"", "unknown"}, {" \r\n ", "unknown"}, {" first \r\nsecond", "first"}} {
		if got := firstLine(c.input); got != c.want {
			t.Fatalf("%q: %q", c.input, got)
		}
	}
	t.Setenv("CUDA_PATH", "")
	t.Setenv("CUDA_HOME", "")
	if _, err := Detect(t.Context(), ""); err == nil {
		t.Fatal("missing toolkit detected")
	}
	if _, err := DetectWithHost(t.Context(), t.TempDir(), toolchain.Toolchain{}); err == nil || !strings.Contains(err.Error(), "E_CUDA_NVCC") {
		t.Fatal(err)
	}
}
