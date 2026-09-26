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
