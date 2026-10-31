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

// DetectWSL resolves a compiler inside the named WSL distribution and returns a
// Toolchain whose runner executes commands through wsl.exe.
func DetectWSL(ctx context.Context, distribution, requested string) (Toolchain, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	wsl, base, err := WSLRunner(distribution)
	if err != nil {
		return Toolchain{}, err
	}
	cxx, err := resolveWSLCompiler(ctx, distribution, requested)
	if err != nil {
		return Toolchain{}, err
	}
	kind := wslCompilerKind(cxx)
	cc, _ := WSLExecutable(ctx, distribution, map[Kind]string{Clang: "clang", GCC: "gcc"}[kind])
	ar, _ := WSLExecutable(ctx, distribution, map[Kind]string{Clang: "llvm-ar", GCC: "ar"}[kind])
	if cc == "" || ar == "" {
		return Toolchain{}, fmt.Errorf("E_WSL_TOOLCHAIN_INCOMPLETE: distribution %q compiler %s requires a matching C compiler and archiver (C=%q, ar=%q)", distribution, cxx, cc, ar)
	}
	version := wslCompilerVersion(ctx, wsl, base, cxx)
	target := wslCompilerTarget(ctx, wsl, base, cxx)
	return Toolchain{Kind: kind, CC: cc, CXX: cxx, Archiver: ar, Linker: cxx, Version: version, Target: target, Runner: wsl, RunnerArgs: base}, nil
}

// resolveWSLCompiler resolves the requested compiler inside a distribution. An
// empty or "auto" request probes clang++ then g++, reporting the probe error
// when neither is installed.
func resolveWSLCompiler(ctx context.Context, distribution, requested string) (string, error) {
	cxx := strings.TrimSpace(requested)
	if cxx == "" || cxx == "auto" {
		resolved, findErr := WSLExecutable(ctx, distribution, "clang++", "g++")
		if resolved == "" {
			return "", fmt.Errorf("E_WSL_TOOLCHAIN_NOT_FOUND: no compiler in distribution %q: %w", distribution, findErr)
		}
		return resolved, nil
	}
	resolved, findErr := WSLExecutable(ctx, distribution, cxx)
	if findErr != nil {
		return "", fmt.Errorf("E_WSL_TOOLCHAIN_NOT_FOUND: compiler %q in distribution %q: %w", cxx, distribution, findErr)
	}
	return resolved, nil
}

// wslCompilerKind classifies a resolved WSL compiler path as Clang or GCC.
func wslCompilerKind(compiler string) Kind {
	if strings.Contains(strings.ToLower(pathpkg.Base(compiler)), "clang") {
		return Clang
	}
	return GCC
}

// wslCompilerVersion returns the first line of a WSL compiler's --version
// output, reporting "unknown" when the probe fails.
func wslCompilerVersion(ctx context.Context, wsl string, base []string, compiler string) string {
	args := append(append([]string{}, base...), "--exec", compiler, "--version")
	out, err := exec.CommandContext(ctx, wsl, args...).CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.Split(string(out), "\n")[0])
}

// wslCompilerTarget reports the machine tuple a WSL compiler builds for,
// defaulting to "wsl" when the probe does not answer.
func wslCompilerTarget(ctx context.Context, wsl string, base []string, compiler string) string {
	args := append(append([]string{}, base...), "--exec", compiler, "-dumpmachine")
	out, err := exec.CommandContext(ctx, wsl, args...).Output()
	if err != nil {
		return "wsl"
	}
	return strings.TrimSpace(string(out))
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
			detail = compactWSLOutput(append(out, exitErr.Stderr...))
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

// WSLPath converts a Windows path to its Linux equivalent inside the
// distribution, checking first that the two agree on the distribution.
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

// WSLWindowsPath converts a Linux path inside the distribution to a Windows
// path the host can use.
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

// ValidateWSLPath rejects a distribution UNC path that contradicts the selected
// distribution, so a path is not silently translated against the wrong root.
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

// WithWSLDirectory returns a copy of the toolchain that starts each command in
// directory inside the distribution.
func WithWSLDirectory(t Toolchain, directory string) Toolchain {
	args := append([]string{}, t.RunnerArgs...)
	args = append(args, "--cd", directory)
	t.RunnerArgs = args
	return t
}
