package vulkan

import (
	"strings"
	"testing"
)

func TestCompileGlslc(t *testing.T) {
	sdk := SDK{Glslc: "glslc"}
	command, err := sdk.Compile(ShaderSpec{Source: "shader.vert", Output: "shader.spv", Stage: Vertex, TargetEnv: "vulkan1.3"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(command.Args, " ")
	if !strings.Contains(text, "-fshader-stage=vert") || !strings.Contains(text, "--target-env=vulkan1.3") {
		t.Fatalf("unexpected glslc command: %s", text)
	}
}

func TestCompileGlslcInWSL(t *testing.T) {
	sdk := SDK{Glslc: "/usr/bin/glslc", Runner: "wsl.exe", RunnerArgs: []string{"-d", "Ubuntu", "--cd", "/mnt/c/project/build/debug"}}
	command, err := sdk.Compile(ShaderSpec{Source: "../../../shaders/main.vert", Output: "bin/main.spv", Stage: Vertex})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(command.Args, " ")
	if command.Exe != "wsl.exe" || !strings.Contains(text, "--exec /usr/bin/glslc") || !strings.Contains(text, "../../../shaders/main.vert") {
		t.Fatalf("unexpected WSL shader command: %s %s", command.Exe, text)
	}
}
