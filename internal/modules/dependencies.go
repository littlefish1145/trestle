package modules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"trestle/internal/processx"
)

// compilerHeaderDeps asks Clang's preprocessor for the files it actually
// opened. -MG also lists generated headers that have not been created yet.
func (scanner CommandScanner) compilerHeaderDeps(ctx context.Context, commandSource, localSource string) ([]string, bool, bool) {
	args := []string{"-std=" + scanner.Standard, "-x", "c++", "-MM", "-MG", "-MT", "trestle", commandSource}
	args = append(args, scanner.Options...)
	executable, args := scanner.invocation(scanner.Compiler, args)
	command := processx.Command(ctx, executable, args...)
	command.Dir = scanner.Directory
	output, err := command.Output()
	if err != nil {
		return nil, false, false
	}
	paths, ok := parseMakeDependencies(string(output))
	if !ok {
		return nil, false, false
	}
	seen := map[string]bool{}
	complete := true
	for _, path := range paths {
		local, ok := scanner.mapDependencyPath(ctx, path)
		if !ok {
			return nil, false, false
		}
		if local == localSource {
			continue
		}
		if _, err := os.Stat(local); err == nil {
			seen[local] = true
		} else {
			complete = false
		}
	}
	return sortedKeys(seen), true, complete
}

// mapDependencyPath translates one dependency path the compiler reported into a
// local, canonical path: through the configured dependency mapper when the path
// is absolute in the toolchain, and relative to the scan directory otherwise.
// The boolean is false when the mapper fails, which abandons the probe.
func (scanner CommandScanner) mapDependencyPath(ctx context.Context, path string) (string, bool) {
	if scanner.DependencyMapper != nil && strings.HasPrefix(path, "/") {
		mapped, err := scanner.DependencyMapper(ctx, path)
		if err != nil {
			return "", false
		}
		return canonicalPath(mapped), true
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(scanner.Directory, filepath.FromSlash(path))
	}
	return canonicalPath(path), true
}

// parseMakeDependencies reads the single target produced by -MT trestle.
// Clang leaves Windows path separators intact but escapes spaces and newlines.
func parseMakeDependencies(output string) ([]string, bool) {
	output = strings.ReplaceAll(output, "\\\r\n", " ")
	output = strings.ReplaceAll(output, "\\\n", " ")
	colon := strings.IndexByte(output, ':')
	if colon < 0 || strings.TrimSpace(output[:colon]) != "trestle" {
		return nil, false
	}
	result := splitMakeDependencies(output[colon+1:])
	return result, len(result) > 0
}

// splitMakeDependencies splits the prerequisite list of a make rule on
// unescaped whitespace, honouring backslash escapes for spaces, tabs, and #.
func splitMakeDependencies(input string) []string {
	var result []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	for i := 0; i < len(input); i++ {
		char := input[i]
		if char == '\\' && i+1 < len(input) && (input[i+1] == ' ' || input[i+1] == '\t' || input[i+1] == '#') {
			i++
			current.WriteByte(input[i])
			continue
		}
		if char == ' ' || char == '\r' || char == '\n' || char == '\t' {
			flush()
			continue
		}
		current.WriteByte(char)
	}
	flush()
	return result
}
