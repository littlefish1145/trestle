package runner

import "testing"

func TestParseStatus(t *testing.T) {
	line := "@@TRESTLE:3:10: 30%:2@@ CXX main.cpp"
	progress, ok := ParseStatus(line)
	if !ok || progress.Finished != 3 || progress.Total != 10 || progress.Percent != 30 || progress.Running != 2 {
		t.Fatalf("unexpected progress: %#v ok=%v", progress, ok)
	}
	if detail := StatusDetail(line); detail != "CXX main.cpp" {
		t.Fatalf("unexpected status detail: %q", detail)
	}
}
