package buildanalyzer

import (
	"path/filepath"
	"testing"
)

func TestImpactAcrossMembersSkipsOrderOnly(t *testing.T) {
	dir := t.TempDir()
	session := &Session{Executions: []*Execution{
		{Directory: filepath.Join(dir, "core"), Graph: &Graph{Complete: true, Edges: []Edge{
			{ID: "compile", Discovered: []string{"../公共.h"}, Outputs: []string{"core.obj"}},
			{ID: "alias", Rule: "phony", Inputs: []string{"core.obj"}, Outputs: []string{"ready"}},
		}}},
		{Directory: filepath.Join(dir, "cli"), Graph: &Graph{Complete: true, Edges: []Edge{
			{ID: "link", Implicit: []string{"../core/ready"}, Outputs: []string{"program.exe"}},
			{ID: "copy", OrderOnly: []string{"program.exe"}, Outputs: []string{"copy.exe"}},
		}}},
	}}
	result, err := session.Impact(dir, []string{"公共.h"})
	if err != nil || !result.Complete || len(result.Edges) != 2 || result.Edges[0].ID != "compile" || result.Edges[1].ID != "link" {
		t.Fatalf("impact: %+v, %v", result, err)
	}
}

func TestExplainWriterFragmentedLines(t *testing.T) {
	run := &Execution{}
	writer := run.ExplainWriter()
	for _, part := range []string{"ninja expl", "ain: command line changed for C:/路径.obj\r\nordinary", " output\nninja explain: bin is dirty"} {
		n, err := writer.Write([]byte(part))
		if n != len(part) || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	writer.Flush()
	if len(run.Explanations) != 2 || run.Explanations[0].Kind != "command_changed" || run.Explanations[1].Kind != "dirty_dependency" {
		t.Fatalf("explanations: %+v", run.Explanations)
	}
}
