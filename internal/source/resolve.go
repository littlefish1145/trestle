package source

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func ignoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", ".trestle", "build", "node_modules", "__pycache__":
		return true
	default:
		return false
	}
}

func isHeader(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".h", ".hh", ".hpp", ".hxx", ".inl", ".ipp":
		return true
	default:
		return false
	}
}

func isCompilationUnit(path string) bool {
	if isHeader(path) {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".c++", ".m", ".mm", ".cu":
		return true
	default:
		return false
	}
}

// languageByExtension maps a lowercased file extension to the language that
// extension holds. Extensions absent from the map report the empty Language.
var languageByExtension = map[string]Language{
	".c":    LanguageC,
	".h":    LanguageHeader,
	".hh":   LanguageHeader,
	".hpp":  LanguageHeader,
	".hxx":  LanguageHeader,
	".inl":  LanguageHeader,
	".ipp":  LanguageHeader,
	".cc":   LanguageCXX,
	".cpp":  LanguageCXX,
	".cxx":  LanguageCXX,
	".c++":  LanguageCXX,
	".ixx":  LanguageCXX,
	".cppm": LanguageCXX,
	".cu":   LanguageCUDA,
	".m":    LanguageObjectiveC,
	".rs":   LanguageRust,
	".go":   LanguageGo,
	".py":   LanguagePython,
	".pyw":  LanguagePython,
	".js":   LanguageJavaScript,
	".jsx":  LanguageJavaScript,
	".mjs":  LanguageJavaScript,
	".cjs":  LanguageJavaScript,
	".ts":   LanguageTypeScript,
	".tsx":  LanguageTypeScript,
	".mts":  LanguageTypeScript,
	".cts":  LanguageTypeScript,
}

// languageFor maps a path's extension to the language it holds, or "" when the
// extension is not recognized.
func languageFor(path string) Language {
	return languageByExtension[strings.ToLower(filepath.Ext(path))]
}

func (p *Project) resolveIncludes(defines map[string]bool) {
	for _, path := range sortedSourcePaths(p.Sources) {
		info := p.Sources[path]
		for index := range info.Dependencies {
			dependency := &info.Dependencies[index]
			if dependency.Kind == LocalInclude {
				p.resolveDependencyInclude(path, info, dependency)
			}
			active, _ := dependency.Condition.Evaluate(defines)
			if !active {
				dependency.Optional = true
			}
		}
		p.Sources[path] = info
	}
}

// sortedSourcePaths returns the project's source paths in lexical order so
// include resolution is deterministic.
func sortedSourcePaths(sources map[string]SourceInfo) []string {
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// resolveDependencyInclude resolves a local include against the project, then
// records the include and consumer edges and propagates the consumer's language
// onto the resolved header.
func (p *Project) resolveDependencyInclude(source string, info SourceInfo, dependency *SourceDependency) {
	resolved := p.resolveInclude(source, dependency.Name)
	if resolved == "" {
		return
	}
	dependency.Resolved = resolved
	p.Includes[source] = appendUnique(p.Includes[source], resolved)
	p.Consumers[resolved] = appendUnique(p.Consumers[resolved], source)
	included, exists := p.Sources[resolved]
	if !exists || included.Language != LanguageHeader {
		return
	}
	consumerLanguage := info.Language
	if consumerLanguage == LanguageHeader {
		consumerLanguage = info.InferredLanguage
	}
	included.InferredLanguage = inferIncludedLanguage(included.InferredLanguage, consumerLanguage)
	p.Sources[resolved] = included
}

// inferIncludedLanguage returns the inferred language for an included header:
// the consumer's language when the header has none yet, C++ when the two
// disagree across the C/C++ boundary, and otherwise the header's own value.
func inferIncludedLanguage(included, consumerLanguage Language) Language {
	if included == "" {
		return consumerLanguage
	}
	if included != consumerLanguage && (included == LanguageCXX || consumerLanguage == LanguageCXX) {
		return LanguageCXX
	}
	return included
}

func (p *Project) inferEntryTargets() {
	projectName := filepath.Base(p.Root)
	used := map[string]bool{}
	for path, info := range p.Sources {
		for index := range info.EntryPoints {
			entry := &info.EntryPoints[index]
			target := ""
			if isMainSource(path) {
				target = projectName
			} else {
				target = targetHint(path, projectName)
				if target == "" {
					target = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				}
			}
			base := target
			for suffix := 2; used[target]; suffix++ {
				target = base + "-" + string(rune('0'+suffix-1))
			}
			used[target] = true
			entry.InferredTarget = target
		}
		p.Sources[path] = info
	}
}

func isMainSource(path string) bool {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), filepath.Ext(path))
	return base == "main"
}

func targetHint(path, projectName string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	best := ""
	prefix := strings.ToLower(projectName)
	for _, current := range lexAll(data) {
		if current.Kind != tokenString || !strings.HasPrefix(strings.ToLower(current.Text), prefix) || current.Text == projectName {
			continue
		}
		if !isTargetName(current.Text) {
			continue
		}
		if best == "" || len(current.Text) < len(best) {
			best = current.Text
		}
	}
	return best
}

func isTargetName(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if index == 0 && !isIdentifierStart(value[index]) || index > 0 && !isIdentifierPart(value[index]) {
			return false
		}
	}
	return true
}

func (p Project) resolveInclude(source, name string) string {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleanName) {
		return cleanExistingFile(cleanName)
	}
	candidates := []string{filepath.Join(filepath.Dir(source), cleanName), filepath.Join(p.Root, cleanName), filepath.Join(p.Root, "include", cleanName)}
	for _, candidate := range candidates {
		if resolved := cleanExistingFile(candidate); resolved != "" {
			return resolved
		}
	}
	if runtime.GOOS == "windows" {
		directories := []string{filepath.Dir(source), p.Root, filepath.Join(p.Root, "include")}
		return findCaseInsensitiveInclude(directories, cleanName)
	}
	return ""
}

// cleanExistingFile returns the cleaned path when it names an existing file, and
// "" otherwise.
func cleanExistingFile(path string) string {
	if isFile(path) {
		return filepath.Clean(path)
	}
	return ""
}

// findCaseInsensitiveInclude returns the existing file under one of the
// directories whose name matches cleanName ignoring case, which Windows headers
// require, or "" when none matches.
func findCaseInsensitiveInclude(directories []string, cleanName string) string {
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Clean(filepath.Join(directory, entry.Name())), filepath.Clean(filepath.Join(directory, cleanName))) {
				continue
			}
			return filepath.Join(directory, entry.Name())
		}
	}
	return ""
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
