package diag

import (
	"errors"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	var output strings.Builder
	Render(&output, New("E_TEST", StageBuild, "failed", errors.New("cause")))
	text := output.String()
	if !strings.Contains(text, "error[E_TEST]") || !strings.Contains(text, "stage: build") {
		t.Fatalf("unexpected diagnostic: %s", text)
	}
}
