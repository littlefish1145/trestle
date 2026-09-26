package config

const CurrentSchemaVersion = 5
const DefaultFileName = "trestle.toml"

type Config struct {
	SchemaVersion   int                       `toml:"schema_version"`
	Project         Project                   `toml:"project"`
	Build           Build                     `toml:"build"`
	Toolchain       Toolchain                 `toml:"toolchain"`
	Vcpkg           Vcpkg                     `toml:"vcpkg"`
	CompilerPresets map[string]CompilerPreset `toml:"compiler_presets"`
	Targets         map[string]Target         `toml:"targets"`
	Packages        map[string]Package        `toml:"packages"`
	Package         PackageOutput             `toml:"package"`
}

type Project struct {
	Name string `toml:"name"`
}

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

type Toolchain struct {
	C                     string   `toml:"c"`
	CXX                   string   `toml:"cxx"`
	Archiver              string   `toml:"archiver"`
	Linker                string   `toml:"linker"`
	Setup                 string   `toml:"setup"`
	CUDA                  string   `toml:"cuda"`
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

type Vcpkg struct {
	Root           string `toml:"root"`
	Triplet        string `toml:"triplet"`
	CRTLinkage     string `toml:"crt_linkage"`
	LibraryLinkage string `toml:"library_linkage"`
}

type Target struct {
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

type Dependency struct {
	Target  string `toml:"target"`
	Package string `toml:"package"`
	Scope   string `toml:"scope"`
}

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

type PackageOutput struct {
	Format       string   `toml:"format"`
	Output       string   `toml:"output"`
	Optimization string   `toml:"optimization"`
	Targets      []string `toml:"targets"`
}
