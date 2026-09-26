package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Language string

const (
	LanguageC          Language = "c"
	LanguageCXX        Language = "c++"
	LanguageHeader     Language = "headers"
	LanguageCUDA       Language = "cuda"
	LanguageObjectiveC Language = "objective-c"
	LanguageRust       Language = "rust"
	LanguageGo         Language = "go"
	LanguagePython     Language = "python"
	LanguageJavaScript Language = "javascript"
	LanguageTypeScript Language = "typescript"
)

type DependencyKind string

const (
	LocalInclude      DependencyKind = "local-include"
	SystemInclude     DependencyKind = "system-include"
	Import            DependencyKind = "import"
	ModuleImport      DependencyKind = "module-import"
	DynamicInclude    DependencyKind = "dynamic-include"
	RuntimeImport     DependencyKind = "runtime-import"
	GeneratedInput    DependencyKind = "generated-input"
	UnresolvedInclude DependencyKind = "unresolved-include"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

type Position struct {
	Offset int
	Line   int
	Column int
}

type Range struct {
	Start Position
	End   Position
}

type ConditionKind string

const (
	ConditionTrue       ConditionKind = "true"
	ConditionDefined    ConditionKind = "defined"
	ConditionIdentifier ConditionKind = "identifier"
	ConditionNot        ConditionKind = "not"
	ConditionAnd        ConditionKind = "and"
	ConditionOr         ConditionKind = "or"
	ConditionEqual      ConditionKind = "equal"
	ConditionNotEqual   ConditionKind = "not-equal"
	ConditionUnknown    ConditionKind = "unknown"
)

type Condition struct {
	Kind     ConditionKind
	Value    string
	Children []Condition
}

func (c Condition) Evaluate(defines map[string]bool) (bool, bool) {
	switch c.Kind {
	case ConditionTrue:
		return true, true
	case ConditionDefined:
		return defines[c.Value], true
	case ConditionIdentifier:
		value, known := defines[c.Value]
		return value, known
	case ConditionNot:
		if len(c.Children) == 0 {
			return false, false
		}
		value, known := c.Children[0].Evaluate(defines)
		return !value, known
	case ConditionAnd, ConditionOr:
		if len(c.Children) < 2 {
			return false, false
		}
		left, leftKnown := c.Children[0].Evaluate(defines)
		right, rightKnown := c.Children[1].Evaluate(defines)
		if c.Kind == ConditionAnd {
			return left && right, leftKnown && rightKnown
		}
		return left || right, leftKnown && rightKnown
	case ConditionEqual, ConditionNotEqual:
		if len(c.Children) == 0 {
			return false, false
		}
		left, leftKnown := defines[c.Value]
		right, rightKnown := defines[c.Children[0].Value]
		equal := left == right
		if c.Kind == ConditionNotEqual {
			return !equal, true
		}
		return equal, leftKnown || rightKnown
	default:
		return false, false
	}
}

type SourceDependency struct {
	Kind      DependencyKind
	Name      string
	Range     Range
	Condition Condition
	Optional  bool
	Resolved  string
}

type SymbolInfo struct {
	Name  string
	Kind  string
	Range Range
}

type TestInfo struct {
	Name      string
	Framework string
	Range     Range
	Symbol    string
}

type EntryPoint struct {
	Name           string
	Kind           string
	InferredTarget string
	Range          Range
}

type ModuleInfo struct {
	Provides    string
	Imports     []string
	IsInterface bool
	IsPartition bool
}

type GeneratedHint struct {
	Kind   string
	Target string
	Range  Range
}

type SourceInfo struct {
	Path             string
	Language         Language
	InferredLanguage Language
	Dependencies     []SourceDependency
	Symbols          []SymbolInfo
	Tests            []TestInfo
	EntryPoints      []EntryPoint
	Module           *ModuleInfo
	Generated        []GeneratedHint
	Confidence       Confidence
	ContentHash      string
	ParseError       string
}

type Project struct {
	Root      string
	Sources   map[string]SourceInfo
	Languages map[Language]int
	Includes  map[string][]string
	Consumers map[string][]string
}

type Request struct {
	Path        string
	Defines     []string
	IncludeDirs []string
	Language    Language
}

type Parser interface {
	Accepts(path string) bool
	Parse(request Request) (SourceInfo, error)
}

type Registry struct {
	parsers []Parser
}

func NewRegistry() *Registry {
	return &Registry{parsers: []Parser{&CFamilyParser{}, &RustParser{}, &GoParser{}, &PythonParser{}, &JavaScriptParser{}}}
}

func (r *Registry) Register(parser Parser) {
	r.parsers = append(r.parsers, parser)
}

func (r *Registry) Parse(request Request) (SourceInfo, error) {
	for _, parser := range r.parsers {
		if parser.Accepts(request.Path) {
			return parser.Parse(request)
		}
	}
	return SourceInfo{}, fmt.Errorf("unsupported source %q", request.Path)
}

func Analyze(root string, defines ...string) (Project, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Project{}, err
	}
	project := Project{
		Root:      absoluteRoot,
		Sources:   map[string]SourceInfo{},
		Languages: map[Language]int{},
		Includes:  map[string][]string{},
		Consumers: map[string][]string{},
	}
	registry := NewRegistry()
	defined := map[string]bool{}
	for _, define := range defines {
		name := define
		if offset := indexByte(name, '='); offset >= 0 {
			name = name[:offset]
		}
		defined[name] = true
	}
	err = filepath.WalkDir(absoluteRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != absoluteRoot && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		clean := filepath.Clean(path)
		request := Request{Path: clean, Defines: defines, IncludeDirs: []string{filepath.Dir(clean)}}
		for _, parser := range registry.parsers {
			if parser.Accepts(clean) {
				info, parseErr := parser.Parse(request)
				if parseErr != nil {
					return fmt.Errorf("parse %s: %w", clean, parseErr)
				}
				project.Sources[clean] = info
				project.Languages[info.Language]++
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	project.resolveIncludes(defined)
	project.inferEntryTargets()
	return project, nil
}

func AnalyzeFile(path string, defines ...string) (SourceInfo, error) {
	project, err := Analyze(filepath.Dir(path), defines...)
	if err != nil {
		return SourceInfo{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return SourceInfo{}, err
	}
	info, ok := project.Sources[filepath.Clean(absolute)]
	if !ok {
		return SourceInfo{}, fmt.Errorf("unsupported source %q", path)
	}
	return info, nil
}

func (p Project) CompilationUnits() []string {
	result := make([]string, 0)
	for path := range p.Sources {
		if isCompilationUnit(path) {
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}

func (p Project) MainSources() []string {
	result := make([]string, 0)
	for path, info := range p.Sources {
		for _, entry := range info.EntryPoints {
			if entry.Kind == "Executable" {
				result = append(result, path)
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func indexByte(value string, target byte) int {
	for index := 0; index < len(value); index++ {
		if value[index] == target {
			return index
		}
	}
	return -1
}
