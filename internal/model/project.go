// Package model is the resolved, provider-independent build graph: targets with
// their sources and usage requirements, the dependencies between them, and the
// flattened data a build backend needs to compile and link the project.
package model

// TargetID is the unique name identifying a target within one project.
type TargetID string

// TargetType is the kind of artifact a target produces.
type TargetType string

// The target artifact kinds: static and shared libraries, executables, test
// binaries and shader modules.
const (
	StaticLibrary TargetType = "static"
	SharedLibrary TargetType = "shared"
	Executable    TargetType = "executable"
	Test          TargetType = "test"
	Shader        TargetType = "shader"
)

// DependencyScope describes how widely a dependency's usage requirements are
// re-exported to dependents.
type DependencyScope string

// The dependency scopes: Private applies to the target alone, Public to the
// target and its consumers, and Interface to consumers only.
const (
	Private   DependencyScope = "private"
	Public    DependencyScope = "public"
	Interface DependencyScope = "interface"
)

// DependencyRef points at one dependency source, either another target or an
// external package.
type DependencyRef struct {
	Target  TargetID
	Package string
}

// Dependency pairs a dependency reference with the scope in which it applies.
type Dependency struct {
	Ref   DependencyRef
	Scope DependencyScope
}

// CompileUsage is the set of include directories, preprocessor defines and
// compiler options needed to compile against something.
type CompileUsage struct {
	IncludeDirs []string
	Defines     []string
	Options     []string
}

// LinkItem is a single thing to link: a library named directly or by path, or a
// raw linker option classified by Kind.
type LinkItem struct {
	Kind string
	Name string
	Path string
}

// LinkUsage collects everything needed to link against a target or package:
// library search directories, link items, linker options and runtime files.
type LinkUsage struct {
	LibraryDirs  []string
	Items        []LinkItem
	Options      []string
	RuntimeFiles []string
}

// Usage is the complete set of compile and link requirements something exposes
// to its consumers.
type Usage struct {
	Compile CompileUsage
	Link    LinkUsage
}

// Target is a build target with its sources, artifact settings and the usage
// requirements it exposes at each visibility level.
type Target struct {
	ID               TargetID
	Type             TargetType
	Sources          []string
	OutputName       string
	CStandard        string
	CXXStandard      string
	CFlags           []string
	CXXFlags         []string
	ExportAllSymbols bool
	ShaderStage      string
	ShaderEntry      string
	ShaderTool       string
	ShaderTargetEnv  string
	Private          Usage
	Public           Usage
	Interface        Usage
	Implementation   []Dependency
}

// Project is a fully resolved project: its name and every buildable target.
type Project struct {
	Name    string
	Targets []Target
}

// ResolvedTarget embeds a resolved Target plus the self and interface usages the
// link closure has accumulated for it.
type ResolvedTarget struct {
	Target
	CompileSelf      CompileUsage
	CompileInterface CompileUsage
	LinkSelf         LinkUsage
	LinkInterface    LinkUsage
}

// ResolvedProject is the final graph handed to a build backend, indexing targets
// by ID and recording the link closure of each.
type ResolvedProject struct {
	Name        string
	Targets     []ResolvedTarget
	ByID        map[TargetID]ResolvedTarget
	LinkClosure map[TargetID][]TargetID
}
