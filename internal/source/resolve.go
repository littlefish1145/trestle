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

func languageFor(path string) Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c":
		return LanguageC
	case ".h", ".hh", ".hpp", ".hxx", ".inl", ".ipp":
		return LanguageHeader
	case ".cc", ".cpp", ".cxx", ".c++", ".ixx", ".cppm":
		return LanguageCXX
	case ".cu":
		return LanguageCUDA
	case ".m":
		return LanguageObjectiveC
	case ".rs":
		return LanguageRust
	case ".go":
		return LanguageGo
	case ".py", ".pyw":
		return LanguagePython
	case ".js", ".jsx", ".mjs", ".cjs":
		return LanguageJavaScript
	case ".ts", ".tsx", ".mts", ".cts":
		return LanguageTypeScript
	default:
		return ""
	}
}

func (p *Project) resolveIncludes(defines map[string]bool) {
	paths := make([]string, 0, len(p.Sources))
	for path := range p.Sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		info := p.Sources[path]
		for index := range info.Dependencies {
			dependency := &info.Dependencies[index]
			if dependency.Kind != LocalInclude {
				continue
			}
			resolved := p.resolveInclude(path, dependency.Name)
			if resolved != "" {
				dependency.Resolved = resolved
				p.Includes[path] = appendUnique(p.Includes[path], resolved)
				p.Consumers[resolved] = appendUnique(p.Consumers[resolved], path)
				if included, exists := p.Sources[resolved]; exists && included.Language == LanguageHeader {
					consumerLanguage := info.Language
					if consumerLanguage == LanguageHeader {
						consumerLanguage = info.InferredLanguage
					}
					if included.InferredLanguage == "" {
						included.InferredLanguage = consumerLanguage
					} else if included.InferredLanguage != consumerLanguage && (included.InferredLanguage == LanguageCXX || consumerLanguage == LanguageCXX) {
						included.InferredLanguage = LanguageCXX
					}
					p.Sources[resolved] = included
				}
			}
			active, _ := dependency.Condition.Evaluate(defines)
			if !active {
				dependency.Optional = true
			}
		}
		p.Sources[path] = info
	}
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
		if isFile(cleanName) {
			return filepath.Clean(cleanName)
		}
		return ""
	}
	candidates := []string{filepath.Join(filepath.Dir(source), cleanName), filepath.Join(p.Root, cleanName), filepath.Join(p.Root, "include", cleanName)}
	for _, candidate := range candidates {
		if isFile(candidate) {
			return filepath.Clean(candidate)
		}
	}
	if runtime.GOOS == "windows" {
		for _, directory := range []string{filepath.Dir(source), p.Root, filepath.Join(p.Root, "include")} {
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
