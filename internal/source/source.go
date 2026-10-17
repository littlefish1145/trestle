// Package source scans a project's files and extracts the information a build
// needs: the language of each file, the dependencies and symbols it declares,
// and how files include or import one another.
package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Language identifies the programming language a source file is written in.
type Language string

// LanguageC and the other Language constants enumerate the languages the
// parsers recognise.
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

// DependencyKind classifies how one file refers to another, which lets the
// build plan treat includes and imports differently.
type DependencyKind string

// LocalInclude and the other DependencyKind constants name the kinds of
// dependency edge the parsers emit.
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

// Confidence expresses how sure a parser is about the dependencies it found,
// with low meaning the result should be treated as a hint rather than fact.
type Confidence string

// ConfidenceHigh and the other Confidence constants grade the reliability of a
// parsed result.
const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Position is a byte offset paired with a 1-based line and column, used to point
// back into the file a token came from.
type Position struct {
	Offset int
	Line   int
	Column int
}

// Range is the span a token, dependency, or symbol occupies in a source file,
// running from Start up to but not including End.
type Range struct {
	Start Position
	End   Position
}

// ConditionKind identifies the shape of a preprocessor condition so it can be
// evaluated against a set of defines.
type ConditionKind string

// ConditionTrue and the other ConditionKind constants enumerate the condition
// nodes produced by the C-family preprocessor evaluator.
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

// Condition is a parsed #if-style expression; nested operands are held in
// Children and Value carries the operand for leaf nodes.
type Condition struct {
	Kind     ConditionKind
	Value    string
	Children []Condition
}

// Evaluate reports whether the condition holds under the given defines and
// whether that answer is certain. The second result is false when the
// expression depends on a value Trestle cannot know, so callers can downgrade
// their confidence instead of guessing.
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

// SourceDependency records a single edge from a source file to something it
// references, such as a header, module, or package.
type SourceDependency struct {
	Kind      DependencyKind
	Name      string
	Range     Range
	Condition Condition
	Optional  bool
	Resolved  string
}

// SymbolInfo is a declaration the parser noticed, such as a class, function, or
// namespace, together with the span it occupies.
type SymbolInfo struct {
	Name  string
	Kind  string
	Range Range
}

// TestInfo describes a test the parser recognised, including the framework it
// appears to belong to and the symbol it defines.
type TestInfo struct {
	Name      string
	Framework string
	Range     Range
	Symbol    string
}

// EntryPoint is a program entry function such as main, plus the build target the
// analyzer believes it should belong to.
type EntryPoint struct {
	Name           string
	Kind           string
	InferredTarget string
	Range          Range
}

// ModuleInfo summarises the C++20 module declarations in a file: the module it
// provides, the modules it imports, and whether it is an interface unit or a
// partition.
type ModuleInfo struct {
	Provides    string
	Imports     []string
	IsInterface bool
	IsPartition bool
}

// GeneratedHint marks an identifier that looks like a generated artifact, so the
// build can link a source to the step that produces it.
type GeneratedHint struct {
	Kind   string
	Target string
	Range  Range
}

// SourceInfo is everything a parser learned about one file: its language, the
// dependencies and symbols found, and a hash of its content used to detect
// changes.
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

// Project is the result of analyzing a tree of sources, indexing each file and
// the include and consumer relationships between them.
type Project struct {
	Root      string
	Sources   map[string]SourceInfo
	Languages map[Language]int
	Includes  map[string][]string
	Consumers map[string][]string
}

// Request describes the file to parse along with the defines, include
// directories, and language override that affect how it is read.
type Request struct {
	Path        string
	Defines     []string
	IncludeDirs []string
	Language    Language
}

// Parser recognises the files it understands and extracts a SourceInfo from
// them.
type Parser interface {
	Accepts(path string) bool
	Parse(request Request) (SourceInfo, error)
}

// Registry holds the parsers consulted, in order, when a path is analyzed.
type Registry struct {
	parsers []Parser
}

// NewRegistry returns a registry preloaded with the parsers Trestle ships for
// the C family, Rust, Go, Python, and JavaScript/TypeScript.
func NewRegistry() *Registry {
	return &Registry{parsers: []Parser{&CFamilyParser{}, &RustParser{}, &GoParser{}, &PythonParser{}, &JavaScriptParser{}}}
}

// Register appends a parser to the registry; parsers are tried in the order they
// were added.
func (r *Registry) Register(parser Parser) {
	r.parsers = append(r.parsers, parser)
}

// Parse returns the SourceInfo produced by the first registered parser that
// accepts the request's path, or an error when none applies.
func (r *Registry) Parse(request Request) (SourceInfo, error) {
	for _, parser := range r.parsers {
		if parser.Accepts(request.Path) {
			return parser.Parse(request)
		}
	}
	return SourceInfo{}, fmt.Errorf("unsupported source %q", request.Path)
}

// Analyze walks every source under root and builds a Project, skipping vendor
// and build directories. The optional defines seed preprocessor evaluation so
// conditional includes can be judged active or optional.
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

// AnalyzeFile analyzes the directory containing path and returns the entry for
// that one file, which is handy for inspecting a single source.
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

// CompilationUnits lists the paths that can be compiled on their own, that is
// every source except headers, sorted for stable output.
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

// MainSources lists the files that define an executable entry point, sorted for
// stable output.
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
