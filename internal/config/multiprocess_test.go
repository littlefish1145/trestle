package config

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigWriterProcess(t *testing.T) {
	path := os.Getenv("TRESTLE_CONFIG_CHILD")
	if path == "" {
		return
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("snapshot ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	cfg.Project.Name = "child"
	if err := Save(path, cfg); err == nil || !strings.Contains(err.Error(), "E_CONFIG_CONFLICT") {
		t.Fatalf("stale process committed: %v", err)
	}
}

func TestConfigCommitAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	if err := Save(path, Default("parent")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigWriterProcess$")
	cmd.Env = append(os.Environ(), "TRESTLE_CONFIG_CHILD="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "snapshot ready" {
		t.Fatalf("child: %s %v", line, err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Build.Profile = "release"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(stdin, "commit")
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.Project.Name != "parent" || cfg.Build.Profile != "release" {
		t.Fatalf("lost update: %+v %v", cfg, err)
	}
}
