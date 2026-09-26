package toolchain

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func DetectWSL(ctx context.Context, distribution, requested string) (Toolchain, error) {
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return Toolchain{}, fmt.Errorf("E_WSL_NOT_FOUND: wsl.exe was not found")
	}
	base := []string{}
	if distribution != "" {
		base = append(base, "-d", distribution)
	}
	find := func(names ...string) string {
		for _, name := range names {
			args := append(append([]string{}, base...), "--exec", "sh", "-lc", "command -v "+name)
			if out, e := exec.CommandContext(ctx, wsl, args...).Output(); e == nil && strings.TrimSpace(string(out)) != "" {
				return strings.TrimSpace(string(out))
			}
		}
		return ""
	}
	cxx := strings.TrimSpace(requested)
	if cxx == "" || cxx == "auto" {
		cxx = find("clang++", "g++")
	} else if !strings.Contains(cxx, "/") {
		cxx = find(cxx)
	}
	if cxx == "" {
		return Toolchain{}, fmt.Errorf("E_WSL_TOOLCHAIN_NOT_FOUND: no clang++ or g++ compiler was found in WSL")
	}
	kind := GCC
	if strings.Contains(strings.ToLower(filepath.Base(cxx)), "clang") {
		kind = Clang
	}
	cc := find(map[Kind]string{Clang: "clang", GCC: "gcc"}[kind])
	ar := find(map[Kind]string{Clang: "llvm-ar", GCC: "ar"}[kind])
	versionArgs := append(append([]string{}, base...), "--exec", cxx, "--version")
	version := "unknown"
	if out, e := exec.CommandContext(ctx, wsl, versionArgs...).CombinedOutput(); e == nil {
		version = strings.TrimSpace(strings.Split(string(out), "\n")[0])
	}
	return Toolchain{Kind: kind, CC: cc, CXX: cxx, Archiver: ar, Linker: cxx, Version: version, Target: "wsl", Runner: wsl, RunnerArgs: base}, nil
}

func WSLPath(ctx context.Context, distribution, path string) (string, error) {
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return "", err
	}
	args := []string{}
	if distribution != "" {
		args = append(args, "-d", distribution)
	}
	args = append(args, "--exec", "wslpath", "-a", path)
	cmd := exec.CommandContext(ctx, wsl, args...)
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail = compactWSLOutput(exitErr.Stderr)
		}
		return "", fmt.Errorf("convert WSL path: %w: %s", err, detail)
	}
	converted := parseWSLPath(out)
	if converted == "" {
		return "", fmt.Errorf("convert WSL path: wslpath returned no Linux path: %s", compactWSLOutput(out))
	}
	return converted, nil
}

func parseWSLPath(output []byte) string {
	// Some Windows/WSL combinations emit a UTF-16 startup warning before the
	// actual UTF-8 command output. Only a single absolute Linux path is valid.
	text := strings.ReplaceAll(string(output), "\x00", "")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, "/") {
			return line
		}
	}
	return ""
}

func compactWSLOutput(output []byte) string {
	text := strings.ReplaceAll(string(output), "\x00", "")
	return strings.Join(strings.Fields(text), " ")
}

func WithWSLDirectory(t Toolchain, directory string) Toolchain {
	args := append([]string{}, t.RunnerArgs...)
	args = append(args, "--cd", directory)
	t.RunnerArgs = args
	return t
}
