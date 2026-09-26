package app

import (
	"path/filepath"
	"testing"

	"trestle/internal/config"
)

func TestListTestsAndSetGroup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.DefaultFileName)
	cfg := config.Default("suite")
	cfg.Targets = map[string]config.Target{
		"unit_math": {Type: "test", Sources: []string{"tests/math.cpp"}, TestGroup: "unit", TestArgs: []string{"--quick"}},
		"app":       {Type: "executable", Sources: []string{"src/main.cpp"}},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	jobs, err := ListTests(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Name != "unit_math" || jobs[0].Group != "unit" || filepath.Base(jobs[0].Files[0]) != "math.cpp" {
		t.Fatalf("unexpected jobs: %#v", jobs)
	}
	if err := SetTestGroup(path, "unit_math", "smoke"); err != nil {
		t.Fatal(err)
	}
	jobs, err = ListTests(path)
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].Group != "smoke" {
		t.Fatalf("group was not persisted: %#v", jobs[0])
	}
}
