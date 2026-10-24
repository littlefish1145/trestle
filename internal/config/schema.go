// Package config defines trestle's on-disk configuration model, validates it,
// migrates older schema versions to the current one, and reads and writes
// trestle.toml safely.
package config

// CurrentSchemaVersion is the schema revision this build writes. A file carrying
// a newer number is rejected and an older one is migrated up to this version.
const CurrentSchemaVersion = 7

// DefaultFileName is the file trestle looks for when a project directory is
// opened without an explicit configuration path.
const DefaultFileName = "trestle.toml"

// Config is the complete trestle.toml document: project identity, build and
// toolchain settings, dependency providers, targets, rules and tasks. The
// unexported fields record where the document was loaded from so a save can
// detect concurrent edits.
type Config struct {
	sourcePath        string
	sourceFingerprint string
	SchemaVersion     int                       `toml:"schema_version"`
	Project           Project                   `toml:"project"`
	Build             Build                     `toml:"build"`
	Toolchain         Toolchain                 `toml:"toolchain"`
	Vcpkg             Vcpkg                     `toml:"vcpkg"`
	Conan             Conan                     `toml:"conan"`
	CompilerPresets   map[string]CompilerPreset `toml:"compiler_presets"`
	Targets           map[string]Target         `toml:"targets"`
	Packages          map[string]Package        `toml:"packages"`
	Package           PackageOutput             `toml:"package"`
	Rules             []Rule                    `toml:"rules"`
	Workspace         *Workspace                `toml:"workspace,omitempty"`
	Tasks             map[string]Task           `toml:"tasks"`
}

// Project identifies the project, seeding default output names and the default
// target when none is configured.
type Project struct {
	Name string `toml:"name"`
}

// Workspace groups member projects under a shared root. Each member is a
// directory relative to the workspace root and must contain its own
// trestle.toml. Building the workspace root builds every member (or the
// members named on the command line).
type Workspace struct {
	Members []string `toml:"members"`
	// Dependencies declares packages shared by every member. Each entry is
	// named by its port and merged into every member's package set unless the
	// member declares a package with the same name.
	Dependencies []Package `toml:"dependencies,omitempty"`
}

// Build holds settings that apply to every target unless a rule or a target
// overrides them: the build profile, output directory, language standards and
// the global compile and link flags.
type Build struct {
	Profile            string   `toml:"profile"`
	BuildDir           string   `toml:"build_dir"`
	CStandard          string   `toml:"c_standard"`
	CXXStandard        string   `toml:"cxx_standard"`
	CompileFlags       []string `toml:"compile_flags"`
	CFlags             []string `toml:"c_flags"`
	CXXFlags           []string `toml:"cxx_flags"`
	LinkFlags          []string `toml:"link_flags"`
	DefaultTargets     []string `toml:"default_targets"`
	CompileCommands    string   `toml:"compile_commands"`
	AutoCompileShaders bool     `toml:"auto_compile_shaders"`
	Modules            bool     `toml:"modules"`
	ModuleScanner      string   `toml:"module_scanner"`
}

// Toolchain selects tools by portable version constraint rather than by the
// absolute path of one machine's installation. "auto" means "newest installed",
// a dotted number pins one version, and "low~high" accepts an inclusive
// interval such as 14.3~14.5. Path-valued fields remain as escape hatches for
// unusual setups but are never written by trestle itself.
type Toolchain struct {
	MSVC                  string   `toml:"msvc"`
	C                     string   `toml:"c"`
	CXX                   string   `toml:"cxx"`
	Archiver              string   `toml:"archiver"`
	Linker                string   `toml:"linker"`
	Setup                 string   `toml:"setup"`
	CUDA                  string   `toml:"cuda"`
	CUDARoot              string   `toml:"cuda_root"`
	CacheDir              string   `toml:"cache_dir"`
	CUDAMode              string   `toml:"cuda_mode"`
	CUDAArchitectures     []string `toml:"cuda_architectures"`
	CUDAExecution         string   `toml:"cuda_execution"`
	CUDAWSLDistribution   string   `toml:"cuda_wsl_distribution"`
	Vulkan                string   `toml:"vulkan"`
	VulkanExecution       string   `toml:"vulkan_execution"`
	VulkanWSLDistribution string   `toml:"vulkan_wsl_distribution"`
	ModuleScanner         string   `toml:"module_scanner"`
	Mode                  string   `toml:"mode"`
	WSLDistribution       string   `toml:"wsl_distribution"`
	Preset                string   `toml:"preset"`
}

// CompilerPreset is a named, reusable toolchain selection that rules and package
// optimization settings can refer to instead of repeating compiler and flag
// settings.
type CompilerPreset struct {
	C               string   `toml:"c"`
	CXX             string   `toml:"cxx"`
	Archiver        string   `toml:"archiver"`
	Linker          string   `toml:"linker"`
	Setup           string   `toml:"setup"`
	Mode            string   `toml:"mode"`
	WSLDistribution string   `toml:"wsl_distribution"`
	CompileFlags    []string `toml:"compile_flags"`
	CFlags          []string `toml:"c_flags"`
	CXXFlags        []string `toml:"cxx_flags"`
	LinkFlags       []string `toml:"link_flags"`
}

// Vcpkg selects the vcpkg installation and the triplet its ports are built for.
// An empty Root falls back to VCPKG_ROOT, and Triplet may be "auto" to let
// trestle choose one from the target architecture.
type Vcpkg struct {
	Root           string `toml:"root"`
	Triplet        string `toml:"triplet"`
	CRTLinkage     string `toml:"crt_linkage"`
	LibraryLinkage string `toml:"library_linkage"`
}

// Conan selects the Conan 2 installation used by provider "conan" packages.
// Root holds the conan home (cache) directory, BuildRoot is the parent folder
// that receives per-profile conan-<profile> install outputs (defaulting to the
// build directory's parent), and Profile is the profile conan resolves with
// `conan profile detect`. Settings/Options are passed through as -s/-o on
// every install so cross builds stay reproducible.
type Conan struct {
	Root      string            `toml:"root"`
	BuildRoot string            `toml:"build_root"`
	Profile   string            `toml:"profile"`
	Settings  map[string]string `toml:"settings"`
	Options   map[string]string `toml:"options"`
}

// Target is one buildable unit: its sources, artifact type and output name, the
// usage requirements it exposes, and the targets or packages it depends on.
type Target struct {
	When               string       `toml:"when"`
	RequiresTools      []string     `toml:"requires_tools"`
	RequiresWSL        string       `toml:"requires_wsl"`
	Type               string       `toml:"type"`
	Sources            []string     `toml:"sources"`
	OutputName         string       `toml:"output_name"`
	CStandard          string       `toml:"c_standard"`
	CXXStandard        string       `toml:"cxx_standard"`
	ExportAllSymbols   bool         `toml:"export_all_symbols"`
	IncludeDirs        []string     `toml:"include_dirs"`
	PrivateIncludeDirs []string     `toml:"private_include_dirs"`
	Defines            []string     `toml:"defines"`
	PrivateDefines     []string     `toml:"private_defines"`
	CompileOptions     []string     `toml:"compile_options"`
	CFlags             []string     `toml:"c_flags"`
	CXXFlags           []string     `toml:"cxx_flags"`
	LibraryDirs        []string     `toml:"library_dirs"`
	Libraries          []string     `toml:"libraries"`
	LinkOptions        []string     `toml:"link_options"`
	InterfaceCompile   []string     `toml:"interface_compile_options"`
	InterfaceLink      []string     `toml:"interface_link_options"`
	ShaderStage        string       `toml:"shader_stage"`
	ShaderEntry        string       `toml:"shader_entry"`
	ShaderTool         string       `toml:"shader_tool"`
	ShaderTargetEnv    string       `toml:"shader_target_env"`
	Dependencies       []Dependency `toml:"dependencies"`
	TestGroup          string       `toml:"test_group"`
	TestArgs           []string     `toml:"test_args"`
	TestWorkingDir     string       `toml:"test_working_dir"`
}

// Rule applies declarative build changes when its side-effect-free condition is true.
type Rule struct {
	When         string   `toml:"when"`
	Target       string   `toml:"target"`
	C            string   `toml:"c"`
	CXX          string   `toml:"cxx"`
	Mode         string   `toml:"mode"`
	WSL          string   `toml:"wsl_distribution"`
	CompileFlags []string `toml:"compile_flags"`
	CFlags       []string `toml:"c_flags"`
	CXXFlags     []string `toml:"cxx_flags"`
	LinkFlags    []string `toml:"link_flags"`
	Packages     []string `toml:"packages"`
	DependsOn    []string `toml:"depends_on"`
	Targets      []string `toml:"targets"`
}

// Task is an explicitly confirmed argv command followed by whitelisted edits.
type Task struct {
	Description string            `toml:"description"`
	Command     []string          `toml:"command"`
	WorkingDir  string            `toml:"working_dir"`
	Timeout     string            `toml:"timeout"`
	Set         map[string]string `toml:"set"`
}

// TaskSettingChange records one configuration field a task would set, showing
// its current value and the value the task proposes, so a preview can be
// reviewed before the task runs.
type TaskSettingChange struct {
	Field  string
	Before string
	After  string
}

// TaskPreview is the dry-run view of a task: the resolved working directory and
// timeout, the settings it would change, and a fingerprint pinning the project
// state the preview was computed against.
type TaskPreview struct {
	Task        Task
	WorkingDir  string
	Timeout     string
	Changes     []TaskSettingChange
	Fingerprint string
}

// Dependency names one edge in the target graph: exactly one of Target or
// Package is set, and Scope controls how the dependency's usage propagates.
type Dependency struct {
	Target  string `toml:"target"`
	Package string `toml:"package"`
	Scope   string `toml:"scope"`
}

// Package declares a dependency supplied by a package manager or by hand. Port
// is the upstream package name (defaulting to the map key), and the manual
// fields supply usage directly when the provider exposes no metadata.
type Package struct {
	Provider     string   `toml:"provider"`
	Port         string   `toml:"port"`
	Version      string   `toml:"version"`
	Features     []string `toml:"features"`
	Triplet      string   `toml:"triplet"`
	IncludeDirs  []string `toml:"include_dirs"`
	LibraryDirs  []string `toml:"library_dirs"`
	Libraries    []string `toml:"libraries"`
	Defines      []string `toml:"defines"`
	RuntimeFiles []string `toml:"runtime_files"`
}

// PackageOutput configures the distributable archive built by the package
// command: its format, destination, optimization preset and included targets.
type PackageOutput struct {
	Format       string   `toml:"format"`
	Output       string   `toml:"output"`
	Optimization string   `toml:"optimization"`
	Targets      []string `toml:"targets"`
}
