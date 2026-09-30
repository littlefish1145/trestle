package modules

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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
		if scanner.DependencyMapper != nil && strings.HasPrefix(path, "/") {
			path, err = scanner.DependencyMapper(ctx, path)
			if err != nil {
				return nil, false, false
			}
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(scanner.Directory, filepath.FromSlash(path))
		}
		path = canonicalPath(path)
		if path != localSource {
			if _, err := os.Stat(path); err == nil {
				seen[path] = true
			} else {
				complete = false
			}
		}
	}
	result := make([]string, 0, len(seen))
	for path := range seen {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, true, complete
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
	var result []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	input := output[colon+1:]
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
	return result, len(result) > 0
}
