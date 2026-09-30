package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"
)

func DetectWSL(ctx context.Context, distribution, requested string) (Toolchain, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return Toolchain{}, err
	}
	var lookupErr error
	find := func(names ...string) string {
		resolved, err := WSLExecutable(ctx, distribution, names...)
		lookupErr = err
		return resolved
	}
	cxx := strings.TrimSpace(requested)
	if cxx == "" || cxx == "auto" {
		cxx = find("clang++", "g++")
	} else {
		resolved, findErr := WSLExecutable(ctx, distribution, cxx)
		if findErr != nil {
			return Toolchain{}, fmt.Errorf("E_WSL_TOOLCHAIN_NOT_FOUND: compiler %q in distribution %q: %w", cxx, distribution, findErr)
		}
		cxx = resolved
	}
	if cxx == "" {
		return Toolchain{}, fmt.Errorf("E_WSL_TOOLCHAIN_NOT_FOUND: no compiler in distribution %q: %w", distribution, lookupErr)
	}
	kind := GCC
	if strings.Contains(strings.ToLower(pathpkg.Base(cxx)), "clang") {
		kind = Clang
	}
	cc := find(map[Kind]string{Clang: "clang", GCC: "gcc"}[kind])
	ar := find(map[Kind]string{Clang: "llvm-ar", GCC: "ar"}[kind])
	if cc == "" || ar == "" {
		return Toolchain{}, fmt.Errorf("E_WSL_TOOLCHAIN_INCOMPLETE: distribution %q compiler %s requires a matching C compiler and archiver (C=%q, ar=%q)", distribution, cxx, cc, ar)
	}
	versionArgs := append(append([]string{}, base...), "--exec", cxx, "--version")
	version := "unknown"
	if out, e := exec.CommandContext(ctx, wsl, versionArgs...).CombinedOutput(); e == nil {
		version = strings.TrimSpace(strings.Split(string(out), "\n")[0])
	}
	target := "wsl"
	args := append(append([]string{}, base...), "--exec", cxx, "-dumpmachine")
	if out, err := exec.CommandContext(ctx, wsl, args...).Output(); err == nil {
		target = strings.TrimSpace(string(out))
	}
	return Toolchain{Kind: kind, CC: cc, CXX: cxx, Archiver: ar, Linker: cxx, Version: version, Target: target, Runner: wsl, RunnerArgs: base}, nil
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
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return "", err
	}
	detail := ""
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		args := append(append([]string{}, base...), "--exec", "sh", "-lc", `if [ -x "$1" ]; then printf '%s\n' "$1"; else command -v -- "$1"; fi`, "trestle", name)
		if out, runErr := exec.CommandContext(ctx, wsl, args...).Output(); runErr == nil {
			if resolved := parseWSLPath(out); resolved != "" {
				return resolved, nil
			}
		} else if ctx.Err() != nil {
			return "", fmt.Errorf("WSL probe %q in %q: %w", name, distribution, ctx.Err())
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			detail = compactWSLOutput(exitErr.Stderr)
		}
	}
	return "", fmt.Errorf("command not found in WSL distribution %q: %s; %s", distribution, strings.Join(names, ", "), detail)
}

// WSLVariable reads a single environment variable from the selected
// distribution without evaluating its value as shell input.
func WSLVariable(ctx context.Context, distribution, name string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
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
	if err := ValidateWSLPath(distribution, path); err != nil {
		return "", err
	}
	if strings.HasPrefix(path, "/") {
		return pathpkg.Clean(path), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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

func WSLWindowsPath(ctx context.Context, distribution, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return "", err
	}
	args := append(append([]string{}, base...), "--exec", "wslpath", "-w", path)
	out, err := exec.CommandContext(ctx, wsl, args...).Output()
	if err != nil {
		return "", fmt.Errorf("convert Linux path %q to Windows: %w", path, err)
	}
	for _, line := range strings.Split(decodeWSLOutput(out), "\n") {
		line = strings.TrimSpace(line)
		if filepath.VolumeName(line) != "" {
			return filepath.Clean(line), nil
		}
	}
	return "", fmt.Errorf("convert Linux path %q to Windows: wslpath returned no Windows path", path)
}

func ValidateWSLPath(distribution, value string) error {
	parts := strings.Split(strings.TrimPrefix(strings.ReplaceAll(value, `\`, `/`), "//"), "/")
	if len(parts) > 1 && (strings.EqualFold(parts[0], "wsl.localhost") || strings.EqualFold(parts[0], "wsl$")) && distribution == "" {
		return fmt.Errorf("E_WSL_PATH_DISTRIBUTION: path %q names distribution %q; configure --wsl-distribution explicitly before using a distribution UNC path", value, parts[1])
	}
	if len(parts) > 1 && (strings.EqualFold(parts[0], "wsl.localhost") || strings.EqualFold(parts[0], "wsl$")) && distribution != "" && !strings.EqualFold(parts[1], distribution) {
		return fmt.Errorf("E_WSL_PATH_DISTRIBUTION: path %q belongs to %q, selected distribution is %q", value, parts[1], distribution)
	}
	return nil
}

func parseWSLPath(output []byte) string {
	// Some Windows/WSL combinations emit a UTF-16 startup warning before the
	// actual UTF-8 command output. Only a single absolute Linux path is valid.
	text := decodeWSLOutput(output)
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
	text := decodeWSLOutput(output)
	return strings.Join(strings.Fields(text), " ")
}

func decodeWSLOutput(output []byte) string {
	if len(output) > 1 && (bytes.HasPrefix(output, []byte{0xff, 0xfe}) || bytes.HasSuffix(output, []byte{10, 0}) || output[len(output)-1] == 0 && output[1] == 0) {
		return strings.TrimPrefix(decodeWindowsCommand(output), "\ufeff")
	}
	return strings.ReplaceAll(string(output), "\x00", "")
}

func WithWSLDirectory(t Toolchain, directory string) Toolchain {
	args := append([]string{}, t.RunnerArgs...)
	args = append(args, "--cd", directory)
	t.RunnerArgs = args
	return t
}
