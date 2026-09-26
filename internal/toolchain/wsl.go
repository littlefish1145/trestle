package toolchain

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func DetectWSL(ctx context.Context, distribution, requested string) (Toolchain, error) {
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return Toolchain{}, err
	}
	find := func(names ...string) string {
		resolved, _ := WSLExecutable(ctx, distribution, names...)
		return resolved
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

// WSLRunner resolves wsl.exe and returns the arguments that select a
// distribution. SDK packages use the same runner contract as compilers.
func WSLRunner(distribution string) (string, []string, error) {
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return "", nil, fmt.Errorf("E_WSL_NOT_FOUND: wsl.exe was not found")
	}
	base := []string{}
	if strings.TrimSpace(distribution) != "" {
		base = append(base, "-d", strings.TrimSpace(distribution))
	}
	return wsl, base, nil
}

// WSLExecutable finds the first command in a distribution. Names are invoked
// as positional shell parameters so configured values are never interpolated
// into the shell program.
func WSLExecutable(ctx context.Context, distribution string, names ...string) (string, error) {
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		args := append(append([]string{}, base...), "--exec", "sh", "-lc", `if [ -x "$1" ]; then printf '%s\n' "$1"; else command -v -- "$1"; fi`, "trestle", name)
		if out, runErr := exec.CommandContext(ctx, wsl, args...).Output(); runErr == nil {
			if resolved := strings.TrimSpace(string(out)); resolved != "" {
				return resolved, nil
			}
		}
	}
	return "", fmt.Errorf("command not found in WSL: %s", strings.Join(names, ", "))
}

// WSLVariable reads a single environment variable from the selected
// distribution without evaluating its value as shell input.
func WSLVariable(ctx context.Context, distribution, name string) string {
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return ""
	}
	args := append(append([]string{}, base...), "--exec", "sh", "-lc", `printenv "$1"`, "trestle", name)
	out, err := exec.CommandContext(ctx, wsl, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
