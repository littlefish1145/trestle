package model

type TargetID string

type TargetType string

const (
	StaticLibrary TargetType = "static"
	SharedLibrary TargetType = "shared"
	Executable    TargetType = "executable"
	Test          TargetType = "test"
	Shader        TargetType = "shader"
)

type DependencyScope string

const (
	Private   DependencyScope = "private"
	Public    DependencyScope = "public"
	Interface DependencyScope = "interface"
)

type DependencyRef struct {
	Target  TargetID
	Package string
}

type Dependency struct {
	Ref   DependencyRef
	Scope DependencyScope
}

type CompileUsage struct {
	IncludeDirs []string
	Defines     []string
	Options     []string
}

type LinkItem struct {
	Kind string
	Name string
	Path string
}

type LinkUsage struct {
	LibraryDirs  []string
	Items        []LinkItem
	Options      []string
	RuntimeFiles []string
}

type Usage struct {
	Compile CompileUsage
	Link    LinkUsage
}

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

type Project struct {
	Name    string
	Targets []Target
}

type ResolvedTarget struct {
	Target
	CompileSelf      CompileUsage
	CompileInterface CompileUsage
	LinkSelf         LinkUsage
	LinkInterface    LinkUsage
}

type ResolvedProject struct {
	Name        string
	Targets     []ResolvedTarget
	ByID        map[TargetID]ResolvedTarget
	LinkClosure map[TargetID][]TargetID
}
