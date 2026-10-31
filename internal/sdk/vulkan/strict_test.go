package vulkan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCompileExactShaderArguments(t *testing.T) {
	for _, tool := range []string{"glslc", "glslangValidator", "glslangValidator.exe"} {
		t.Run(tool, func(t *testing.T) {
			sdk := SDK{Glslc: "/sdk space/bin/glslc", Glslang: "/sdk space/bin/" + tool}
			spec := ShaderSpec{Source: "shader space/main.glsl", Output: "bin space/main.spv", Stage: Fragment, EntryPoint: "main", Includes: []string{"include space", "other"}, Defines: []string{"VALUE=1", "TEXT=hello world"}, TargetEnv: "vulkan1.3", Tool: tool, Depfile: "bin space/main.spv.d"}
			if tool == "glslc" {
				spec.Source = "source/main.glsl"
				spec.Output = "bin/main.spv"
				spec.Depfile = "bin/main.spv.d"
				spec.Includes = []string{"include", "other"}
			}
			before := spec
			before.Includes = append([]string(nil), spec.Includes...)
			before.Defines = append([]string(nil), spec.Defines...)
			command, err := sdk.Compile(spec)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			exe := sdk.Glslc
			if tool == "glslc" {
				want = []string{"-fshader-stage=frag", "-o", spec.Output, "-MD", "-MF", spec.Depfile, "-fentry-point=main", "--target-env=vulkan1.3"}
			} else {
				exe = sdk.Glslang
				want = []string{"-V", "-S", "frag", "-o", spec.Output, "--depfile", spec.Depfile, "-e", "main", "--target-env", "vulkan1.3"}
			}
			want = append(want, "-I"+spec.Includes[0], "-Iother", "-DVALUE=1", "-DTEXT=hello world", spec.Source)
			if command.Exe != exe || !reflect.DeepEqual(command.Args, want) {
				t.Fatalf("got %s %#v; want %s %#v", command.Exe, command.Args, exe, want)
			}
			if !reflect.DeepEqual(spec, before) {
				t.Fatal("Compile mutated input slices")
			}
		})
	}
}

func TestShaderToolSelectionAndFailureContracts(t *testing.T) {
	spec := ShaderSpec{Source: "main.vert", Output: "main.spv", Stage: Vertex}
	for _, c := range []struct {
		name       string
		sdk        SDK
		tool, want string
		fail       bool
	}{
		{"prefer glslc", SDK{Glslc: "glslc", Glslang: "glslangValidator"}, "", "glslc", false},
		{"fallback glslang", SDK{Glslang: "glslangValidator"}, "", "glslangValidator", false},
		{"missing glslc selection", SDK{Glslang: "glslangValidator"}, "glslc", "", true},
		{"missing glslang selection", SDK{Glslc: "glslc"}, "glslangValidator", "", true},
		{"explicit executable", SDK{}, "/custom dir/glslangValidator", "/custom dir/glslangValidator", false},
		{"missing all", SDK{}, "", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec.Tool = c.tool
			command, err := c.sdk.Compile(spec)
			if c.fail {
				if err == nil || !strings.Contains(err.Error(), "E_VULKAN_SHADER_TOOL_NOT_FOUND") {
					t.Fatalf("%v", err)
				}
				return
			}
			if err != nil || command.Exe != c.want {
				t.Fatalf("%+v %v", command, err)
			}
		})
	}
	for _, stage := range []Stage{"", "invalid", "vert;echo", "vert\n"} {
		spec.Stage = stage
		if _, err := (SDK{Glslc: "glslc"}).Compile(spec); err == nil || !strings.Contains(err.Error(), "E_VULKAN_SHADER_STAGE") {
			t.Fatalf("stage %q: %v", stage, err)
		}
	}
	spec = ShaderSpec{Stage: Compute, Output: "out"}
	if _, err := (SDK{Glslc: "glslc"}).Compile(spec); err == nil {
		t.Fatal("empty source accepted")
	}
	spec = ShaderSpec{Stage: Compute, Source: "in"}
	if _, err := (SDK{Glslc: "glslc"}).Compile(spec); err == nil {
		t.Fatal("empty output accepted")
	}
}

func TestWSLDirectoryReplacementAndWrapping(t *testing.T) {
	original := SDK{Glslc: "/usr/bin/glslc", Runner: "wsl.exe", RunnerArgs: []string{"-d", "ArchLinux", "--cd", "/old space", "--user", "builder"}}
	before := append([]string(nil), original.RunnerArgs...)
	updated := WithWSLDirectory(original, "/new space")
	updated = WithWSLDirectory(updated, "/final space")
	want := []string{"-d", "ArchLinux", "--user", "builder", "--cd", "/final space"}
	if !reflect.DeepEqual(updated.RunnerArgs, want) || !reflect.DeepEqual(original.RunnerArgs, before) {
		t.Fatalf("runner mutated or duplicated cd: %v", updated.RunnerArgs)
	}
	command, err := updated.Compile(ShaderSpec{Source: "../src/a.comp", Output: "bin/a.spv", Stage: Compute})
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, "--exec", "/usr/bin/glslc", "-fshader-stage=comp", "-o", "bin/a.spv", "../src/a.comp")
	if command.Exe != "wsl.exe" || !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("WSL command: %+v", command)
	}
	if !reflect.DeepEqual(WithWSLDirectory(original, " ").RunnerArgs, before) {
		t.Fatal("empty directory rewrote runner")
	}
	if got := WithWSLDirectory(SDK{Glslc: "glslc"}, "/ignored"); got.Runner != "" || len(got.RunnerArgs) != 0 {
		t.Fatal(got)
	}
}

func TestDetectRootRejectsDirectoriesAndUnrelatedPATHTools(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	name := "glslc"
	bin := "bin"
	if runtime.GOOS == "windows" {
		name += ".exe"
		bin = "Bin"
	}
	if err := os.WriteFile(filepath.Join(other, name), []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	if _, err := DetectRoot(root); err == nil {
		t.Fatal("explicit root used an unrelated PATH tool")
	}
	candidate := filepath.Join(root, bin, name)
	if err := os.MkdirAll(candidate, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectRoot(root); err == nil {
		t.Fatal("directory accepted as shader compiler")
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}
	sdk, err := DetectRoot(root)
	if err != nil || sdk.Glslc != candidate || sdk.IncludeDir != filepath.Join(root, "include") {
		t.Fatalf("%+v %v", sdk, err)
	}
	t.Setenv("VULKAN_SDK", root)
	if _, err := Detect(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VULKAN_SDK", "")
	if _, err := Detect(); err == nil {
		t.Fatal("unset SDK accepted")
	}
	if _, err := DetectRoot(" "); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestStageAndToolBasenameMatrix(t *testing.T) {
	for _, stage := range []Stage{Vertex, Fragment, Compute, Geometry, TessControl, TessEvaluation, "task", "mesh", "rgen", "rint", "rahit", "rchit", "rmiss", "rcall"} {
		if !ValidStage(stage) {
			t.Fatal(stage)
		}
	}
	for _, tool := range []string{"glslangValidator", "/sdk/bin/glslangValidator", `C:\SDK Space\Bin\glslangValidator.exe`} {
		if toolBase(tool) != "glslangvalidator" {
			t.Fatalf("basename %q: %q", tool, toolBase(tool))
		}
	}
	for _, c := range []struct{ input, want string }{{"", "unknown"}, {"  \n", "unknown"}, {" first \r\nsecond", "first"}} {
		if got := firstLine(c.input); got != c.want {
			t.Fatal(got)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := DetectWSL(ctx, "anything", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	sdk := SDK{}
	if _, err := sdk.Link("", []string{"x"}); err == nil {
		t.Fatal("unnamed link accepted")
	}
	if _, err := sdk.Link("x", nil); err == nil {
		t.Fatal("empty link accepted")
	}
	if command, err := sdk.Link("x", []string{"a", "b"}); err != nil || command != "link a b" {
		t.Fatalf("%q %v", command, err)
	}
}

func TestUnsafeGlslcDependencyPathsFallBackOrFailExplicitly(t *testing.T) {
	spec := ShaderSpec{Source: "shader space/main.comp", Output: "out.spv", Depfile: "out.spv.d", Stage: Compute}
	sdk := SDK{Glslc: "glslc", Glslang: "glslangValidator"}
	command, err := sdk.Compile(spec)
	if err != nil || command.Exe != "glslangValidator" {
		t.Fatalf("safe fallback: %+v %v", command, err)
	}
	spec.Tool = "glslc"
	if _, err := sdk.Compile(spec); err == nil || !strings.Contains(err.Error(), "E_VULKAN_DEPFILE_PATH") {
		t.Fatalf("explicit unsafe depfile: %v", err)
	}
	spec.Tool = ""
	sdk.Glslang = ""
	if _, err := sdk.Compile(spec); err == nil {
		t.Fatal("invalid dependency path silently accepted")
	}
	spec.Source = "source.comp"
	spec.Includes = []string{"include space"}
	if _, err := sdk.Compile(spec); err == nil {
		t.Fatal("invalid include path silently accepted")
	}
}
