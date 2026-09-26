# 面向 C/C++/CUDA/Vulkan 的轻量元构建系统生成器：可落地架构设计

## 核心结论与需要修正的前提

你的总体方向是成立的，而且与 Ninja 自身的设计哲学高度一致：Ninja 明确把自己定位成低层“构建汇编器”，复杂决策应由独立生成器提前完成；`.ninja` 文件应该精确描述文件依赖和命令，而不是把配置、工具链选择、条件判断塞进 Ninja。你的生命周期——**读取配置 → 环境探测 → 生成 manifest → 官方 Ninja 执行 → 解析结果 → 退出**——本质上正是 Ninja 官方鼓励的 meta-build 模式。citeturn30view1

我建议把整个产品设计收敛成一句话：

> **这是一个生成静态 Build Plan 的编译环境编排器，而不是第二个 CMake，也不是第二个 Ninja。**

这句话应该成为后续所有功能取舍的边界。

有一个已经确定的技术决策建议立即修正：**2026 年已经没有理由因为 Fortran 模块而依赖 Kitware 的 Ninja `features-for-fortran` 分支。** 上游 Ninja 已经具有 `dyndep`，当前官方手册甚至直接包含 Fortran Modules 的 dyndep 章节；Ninja 将动态依赖列为正式语法能力。旧的 Kitware 工作属于 Ninja 引入该能力过程中的历史背景。citeturn30view1turn1search0

因此不建议：

```text
if ninja_dyndep_version has ".dyndep-1" suffix:
    enable_fortran()
```

而应是：

```text
ninja version/capability probe
        ↓
can parse minimal dyndep manifest?
        ↓ yes
enable Fortran backend
```

也就是说，**版本字符串只作为快速筛选，真实 capability probe 才是最终依据**。尤其不要把某个历史版本后缀当 ABI/功能标识。上游 Ninja 的 dyndep 文件本身有版本声明机制，并已经把 Fortran 作为正式示例。citeturn3view1turn30view1

第二个重要结论是：你的内部架构不要围绕“生成字符串形式的编译命令”设计，而应存在一个**编译器无关的 Build Plan IR**：

```text
ProjectConfig
     ↓
ResolvedProject
     ↓
BuildPlan
  ├─ Actions
  ├─ Artifacts
  ├─ Dependencies
  ├─ UsageRequirements
  └─ ToolchainBindings
     ↓
Ninja Lowering
     ↓
build.ninja
```

这样 GCC 的 `-c -o`、MSVC 的 `/c /Fo`、NVCC 的 `-ccbin`、Clang Modules 的 PCM、MSVC 的 IFC、GCC 的 GCM 都只存在于**后端 lowering** 中，而不会污染项目模型。

第三个重要结论是：**vcpkg 集成不应该建立在“扫描 `lib/` 然后猜库名”这个假设上。** vcpkg 官方确实定义了常见安装布局，比如 triplet 下的 `include/`、`lib/`、`bin/`、`debug/lib/`、pkg-config 目录等，但这些目录是 triplet 共享目录，并不构成一个完整、稳定、机器可读的“链接接口描述”。citeturn7view0

因此我推荐 vcpkg resolver 使用：

```text
pkg-config metadata
        ↓ 找不到
built-in port adapter
        ↓ 找不到
user override
        ↓ 没有
conservative heuristic
        ↓ 不可靠
明确拒绝自动解析
```

而不是试图实现半个 `find_package()`。

第四个关键结论是：**C++ Modules 是整个项目中风险和复杂度最高的部分，不应该进入第一版核心路径。** P1689 本身就是为构建系统在编译前发现 module `provides/requires` 关系设计的；其论文明确指出模块引入了翻译单元之间的顺序关系，构建系统必须在实际编译前得到这些信息。citeturn22view0 但 Clang、MSVC、GCC 在 BMI 形式、消费者参数、scanner CLI 以及 artifact 管理方式上仍然明显不同，因此应该隔离为独立 `ModuleBackend`，而不是把“模块”塞进通用 `Compiler` 接口。

最终推荐的产品边界是：

| 能力 | 定位 |
|---|---|
| C/C++ GCC/Clang/MSVC/MinGW | 核心能力 |
| 官方 Ninja | 唯一构建执行后端 |
| TOML | 用户可读项目配置 |
| TUI | 配置编辑器，不是执行必经路径 |
| vcpkg | 有边界的依赖提供者 |
| Vulkan SDK / shader | SDK + Asset Compiler，而非 C++ Toolchain |
| CUDA | 复合 Toolchain，NVCC 包装 host compiler |
| C++ Modules | 独立 capability，后期实现 |
| Fortran | 后期语言插件，使用上游 Ninja dyndep |
| CMake script / `find_package` 解释器 | 明确不做 |
| install/export/CPack | 第一阶段明确不做 |
| 自己的 scheduler / incremental executor | 永远不做 |

这能有效防止项目最终变成“一个功能少但复杂度接近 CMake 的 CMake”。

## 总体架构、Go 包结构与完整数据流

### 推荐目录结构

我建议采用 **Domain + Plan IR + Adapter**，而不是传统的“config/toolchain/ninja 各自随便互调”。

```text
.
├── cmd/
│   └── forge/
│       └── main.go
│
├── internal/
│   ├── app/
│   │   ├── configure.go
│   │   ├── generate.go
│   │   ├── build.go
│   │   └── services.go
│   │
│   ├── model/
│   │   ├── project.go
│   │   ├── target.go
│   │   ├── dependency.go
│   │   ├── usage.go
│   │   ├── platform.go
│   │   └── package.go
│   │
│   ├── plan/
│   │   ├── plan.go
│   │   ├── action.go
│   │   ├── artifact.go
│   │   ├── command.go
│   │   └── validate.go
│   │
│   ├── config/
│   │   ├── codec.go
│   │   ├── current.go
│   │   ├── validate.go
│   │   ├── migrate.go
│   │   └── schema/
│   │       ├── v1/
│   │       ├── v2/
│   │       └── v3/
│   │
│   ├── graph/
│   │   ├── target_graph.go
│   │   ├── usage_closure.go
│   │   ├── link_closure.go
│   │   └── topo.go
│   │
│   ├── toolchain/
│   │   ├── api.go
│   │   ├── detect.go
│   │   ├── registry.go
│   │   ├── capabilities.go
│   │   ├── gcc/
│   │   ├── clang/
│   │   ├── msvc/
│   │   ├── mingw/
│   │   └── cuda/
│   │
│   ├── sdk/
│   │   └── vulkan/
│   │       ├── detect.go
│   │       ├── sdk.go
│   │       └── shader.go
│   │
│   ├── deps/
│   │   ├── resolver.go
│   │   └── vcpkg/
│   │       ├── client.go
│   │       ├── search.go
│   │       ├── install.go
│   │       ├── layout.go
│   │       ├── triplet.go
│   │       ├── pkgconfig.go
│   │       ├── adapters.go
│   │       └── resolve.go
│   │
│   ├── modules/
│   │   ├── api.go
│   │   ├── p1689/
│   │   │   ├── model.go
│   │   │   └── decode.go
│   │   ├── graph.go
│   │   ├── scan.go
│   │   ├── cache.go
│   │   └── backends/
│   │       ├── clang/
│   │       ├── msvc/
│   │       └── gcc/
│   │
│   ├── ninja/
│   │   ├── ast.go
│   │   ├── escape.go
│   │   ├── lower.go
│   │   ├── emit.go
│   │   └── rules.go
│   │
│   ├── runner/
│   │   └── ninja/
│   │       ├── runner.go
│   │       ├── status.go
│   │       └── output.go
│   │
│   ├── tui/
│   │   ├── model.go
│   │   ├── messages.go
│   │   ├── forms/
│   │   ├── views/
│   │   └── theme/
│   │
│   ├── state/
│   │   ├── fingerprint.go
│   │   ├── generation.go
│   │   └── cache.go
│   │
│   ├── execx/
│   │   ├── executor.go
│   │   └── process.go
│   │
│   └── fsx/
│       ├── atomic.go
│       ├── hash.go
│       └── path.go
│
└── testdata/
    ├── projects/
    ├── p1689/
    ├── manifests/
    └── configs/
```

这里最重要的不是目录名字，而是**依赖方向**：

```text
                 ┌──────── tui
                 │
cmd ─────────── app
                 │
                 ├──── config ───────────────┐
                 │                            │
                 ├──── toolchain adapters ───┤
                 ├──── deps/vcpkg ───────────┤
                 ├──── sdk/vulkan ───────────┤
                 ├──── modules ──────────────┤
                 │                            ↓
                 └──────────────→ model → graph → plan
                                               │
                                               ↓
                                           ninja emitter
                                               │
                                               ↓
                                           ninja runner
```

**绝对禁止的方向**是：

```text
model → vcpkg
model → TOML
model → Bubble Tea
model → Ninja
toolchain → TUI
ninja → toolchain discovery
```

`model`、`graph` 和 `plan` 应该完全不知道配置格式、UI、vcpkg 甚至 Ninja 的存在。

### 为什么要增加 `plan` 层

这是整个设计最值得提前做的一层。

例如：

```go
type BuildPlan struct {
	Actions   []Action
	Defaults  []ArtifactID
	Artifacts map[ArtifactID]Artifact
}

type Action struct {
	ID       ActionID
	Command  Command
	Inputs   []ArtifactID
	Implicit []ArtifactID
	OrderOnly []ArtifactID
	Outputs  []ArtifactID

	Depfile *DepfileSpec
	Pool    string
}

type Command struct {
	Exe  string
	Args []string
	Env  map[string]string
	Dir  string
}
```

Toolchain 不生成 Ninja：

```text
Clang
   ↓
Action

MSVC
   ↓
Action

NVCC
   ↓
Action
```

然后统一：

```text
BuildPlan
   ↓
ninja.Lower()
   ↓
build.ninja
```

这样以后即使你增加：

```text
forge explain
forge graph
forge compile-command foo.cpp
forge dump-plan
```

都不用反解析 `build.ninja`。

Ninja 官方本身强调生成程序应该提前完成高层决策，并让 Ninja 接收精确依赖图；这与“Build Plan → Ninja lowering”的边界非常契合。citeturn30view1

### 推荐完整数据流

```text
                         interactive TTY
                              │
                              ▼
                       ┌─────────────┐
                       │     TUI     │
                       │ huh / tea   │
                       └──────┬──────┘
                              │ save
                              ▼
                       project.toml
                              │
             non-TTY ─────────┘
                              │
                              ▼
                    Decode + Migrate Schema
                              │
                              ▼
                    Normalize + Validate
                              │
                 ┌────────────┴────────────┐
                 ▼                         ▼
         Toolchain Detection        Dependency Resolution
     GCC/Clang/MSVC/CUDA/...             vcpkg
                 │                         │
                 └────────────┬────────────┘
                              ▼
                      ResolvedProject
                              │
                              ▼
                 Usage Requirement Closure
                              │
                              ▼
                  Module scan if enabled
                 P1689 + scan cache
                              │
                              ▼
                         BuildPlan
                              │
                   validation / cycle check
                              │
                              ▼
                       Ninja lowering
                              │
                              ▼
                    atomic build.ninja
                              │
                              ▼
                      official ninja
                              │
                 ┌────────────┴────────────┐
                 ▼                         ▼
          NINJA_STATUS events       build stdout/stderr
                 │                         │
                 └────────────┬────────────┘
                              ▼
                         BuildResult
                              │
                   TUI / plain terminal
```

这里 TUI 永远只作用于配置；CI 从 `project.toml` 直接进入 `Decode`。

### 哪些模块是核心，哪些可替换

| 模块 | 性质 | 是否可替换 | 单测能力 |
|---|---|---:|---:|
| `model` | Domain | 核心 | 极高 |
| `graph` | 传播/拓扑 | 核心 | 极高 |
| `plan` | 构建 IR | 核心 | 极高 |
| `config` | Persistence Adapter | 可替换 | 高 |
| `toolchain/*` | Platform Adapter | 可增加/替换 | 高 |
| `deps/vcpkg` | Dependency Adapter | 可替换 | 高 |
| `modules/p1689` | Protocol | 核心模块能力 | 极高 |
| `modules/backends/*` | Compiler Adapter | 可替换 | 高 |
| `ninja` | 产品核心后端 | v1 不替换 | 极高 |
| `runner/ninja` | Execution Adapter | 官方 Ninja 固定 | 高 |
| `tui` | Presentation | 完全可替换 | 中高 |
| `sdk/vulkan` | SDK Adapter | 可替换 | 高 |

这里的“官方 Ninja 固定”是**产品决策**而不是代码耦合：代码上仍应把 runner 与 manifest emitter 分开。

## 工具链、CUDA 与 Vulkan 的抽象

### 不要设计一个巨型 `Toolchain`

最容易走错的是：

```go
type Toolchain interface {
	CompileC(...)
	CompileCpp(...)
	CompileCUDA(...)
	CompileModule(...)
	CompileShader(...)
	Link(...)
	Archive(...)
	DetectVulkan(...)
	...
}
```

最后 MSVC、GCC、CUDA 每个实现都有一堆不适用方法。

更合理的是 capability composition。

```go
type Toolchain interface {
	Identity() ToolchainIdentity
	Capabilities() Capabilities
	Fingerprint(ctx context.Context) (Fingerprint, error)

	C() (Compiler, bool)
	CXX() (Compiler, bool)
	Linker() Linker
	Archiver() Archiver
}

type ToolchainIdentity struct {
	Kind       ToolchainKind
	Version    Version
	Target     TargetTriple
	Root       string
	Executable string
}

type Capabilities struct {
	C          bool
	CXX        bool
	Modules    bool
	SharedLib  bool
	StaticLib  bool
	ResponseFiles bool
}
```

然后用语义化 spec 屏蔽：

```text
GCC:  -c foo.cpp -o foo.o
MSVC: /c foo.cpp /Fofoo.obj
```

差异。

```go
type CompileSpec struct {
	Language Language
	Source   string
	Output   string

	Standard string

	Includes []IncludeDir
	Defines  []Define

	Debug        bool
	Optimization Optimization
	PIC          bool

	Depfile string

	Extra ExtraFlags
}

type IncludeDir struct {
	Path   string
	System bool
}

type Define struct {
	Name  string
	Value *string
}

type ExtraFlags struct {
	GCC   []string
	Clang []string
	MSVC  []string
	NVCC  []string
}

type Compiler interface {
	Compile(spec CompileSpec) (Command, error)
	DependencyMode() DependencyMode
}
```

于是：

```go
gcc.Compile(spec)
```

产生：

```text
g++ -std=c++23 -Iinclude -DMODE=1 -MMD -MF foo.o.d -c foo.cpp -o foo.o
```

而：

```go
msvc.Compile(spec)
```

产生：

```text
cl.exe /nologo /std:c++latest /Iinclude /DMODE=1 /showIncludes /c foo.cpp /Fofoo.obj
```

Ninja 对 MSVC 有原生 `deps = msvc`/`/showIncludes` 支持，因此 MSVC backend 不必伪造 GCC depfile。citeturn3view2

原则是：

> **上层永远不能拼接 `-I`、`/I`、`-o`、`/Fo`。**

它只能表达：

```go
IncludeDir{Path: "include"}
Output: "foo.o"
```

### Toolchain 探测也要抽象

```go
type Detector interface {
	Detect(
		ctx context.Context,
		req DetectRequest,
	) ([]Candidate, error)
}

type DetectRequest struct {
	HostOS   string
	HostArch string
	Target   *TargetTriple
}

type Candidate struct {
	Identity     ToolchainIdentity
	Capabilities Capabilities
	Environment  map[string]string
	Metadata     map[string]string
}
```

推荐 registry：

```go
type DetectorRegistry struct {
	detectors []Detector
}
```

而不是：

```go
switch runtime.GOOS {
case "windows":
    ...
}
```

散落整个代码库。

MSVC 尤其应该将 VS environment 视为 `Candidate` 的属性：

```go
Candidate{
    Executable: `...\cl.exe`,
    Environment: {
        "INCLUDE": "...",
        "LIB": "...",
        "PATH": "...",
    },
}
```

不要全局修改：

```go
os.Setenv("INCLUDE", ...)
```

否则 TUI 中同时探测多个 VS instance 或 CUDA host pairing 时会产生非常难调试的隐式状态。

### CUDA 应该是组合工具链

NVCC 官方文档明确说明 CUDA 编译过程同时包含 device 编译和 host C++ compiler 阶段；host compilation 会交给一个受支持的 C++ host compiler，而且 `--compiler-bindir` / `-ccbin` 可明确指定该 host compiler。citeturn28view0

所以模型应该直接表达事实：

```go
type CUDAToolchain struct {
	NVCC    NVCC
	Host    Toolchain
	Toolkit CUDAToolkit
}

type NVCC struct {
	Path    string
	Version Version
}

type CUDAToolkit struct {
	Root    string
	Version Version
}
```

CUDA Compiler：

```go
type CUDACompiler struct {
	nvcc string
	host CompilerBinding
}

type CompilerBinding struct {
	Executable string
	Kind       ToolchainKind
	Version    Version
}
```

最终 lowering：

```text
nvcc
  -ccbin <host>
  -c kernel.cu
  -o kernel.o
```

当前 NVCC 文档也明确将 `-ccbin` 定义为 host compiler 所在目录或 executable 的选择方式。citeturn28view0

更重要的是 CUDA host compatibility 不应只做：

```go
if cuda == 12.4 && msvc <= xxx
```

而应：

```text
文档版本规则
    +
实际 probe
```

例如 TUI 选择一个 host compiler 后：

```text
tmp/probe.cu:

__global__ void f() {}
int main() { return 0; }
```

执行：

```text
nvcc -ccbin <candidate> -c probe.cu -o probe.obj
```

得到：

```text
✓ Verified
! Unsupported by version table
✗ Probe failed
```

**绝不要自动追加 `--allow-unsupported-compiler`。** NVIDIA 当前文档明确警告，绕过 host compiler 版本检查后可能出现编译失败甚至运行时执行错误。citeturn28view0

CUDA 还必须在 Build Plan 中容纳：

```text
ordinary CUDA compile
relocatable device code compile
device link
host final link
```

因为 NVCC 官方提供 `-dc`/relocatable device code 和独立 `-dlink` 阶段。citeturn28view0

建议数据结构：

```go
type CUDACompileMode uint8

const (
	CUDAWholeProgram CUDACompileMode = iota
	CUDASeparateCompilation
)

type CUDACompileSpec struct {
	CompileSpec
	Architectures []string
	Mode          CUDACompileMode
}
```

不要等用户需要跨 `.cu` device symbol 才重构整个 Action 模型。

### Vulkan 不应该进入 Toolchain

Vulkan SDK 是：

```text
headers
loader/library
shader tools
validation/runtime tools
```

而不是：

```text
C/C++ compiler family
```

LunarG Vulkan SDK 文档目前同时提供 `glslangValidator` 与 `glslc`；官方教程也特别指出 `glslc` 使用更类似 GCC/Clang 的命令行风格。citeturn26search1

所以应该是：

```go
type SDK interface {
	Identity() SDKIdentity
}

type VulkanSDK interface {
	SDK

	IncludeDirs() []string
	LibraryDirs() []string
	ShaderCompilers() []ShaderCompiler
}

type ShaderCompiler interface {
	CompileShader(ShaderSpec) (Command, error)
}

type ShaderSpec struct {
	Source      string
	Output      string
	Stage       ShaderStage
	EntryPoint  string
	Includes    []string
	Defines     []Define
	TargetEnv   string
}
```

对应 Ninja：

```ninja
rule glslc
  command = $glslc -fshader-stage=$stage -o $out $in

build out/shader.vert.spv: glslc shaders/shader.vert
  stage = vert
```

这样的分离还有一个好处：

```text
MSVC + Vulkan
Clang + Vulkan
MinGW + Vulkan
```

不用创建：

```text
MSVCVulkanToolchain
ClangVulkanToolchain
MinGWVulkanToolchain
```

### Module 能力同样使用单独接口

```go
type ModuleSupport interface {
	Scanner() ModuleScanner

	PlanProvider(
		ctx context.Context,
		spec ModuleCompileSpec,
	) (ModuleCompilePlan, error)

	ConsumerArgs(
		refs []ModuleRef,
	) ([]string, error)
}

type ModuleScanner interface {
	Scan(
		ctx context.Context,
		req ScanRequest,
	) (ScanResult, error)
}
```

Toolchain 可以通过：

```go
type ModuleCapable interface {
	Modules() ModuleSupport
}
```

暴露能力，而不是给所有 Toolchain 塞空方法。

## 依赖传播模型与 vcpkg 边界

### 配置应该表达 usage requirement，而不是直接表达 flags

建议项目配置长成：

```toml
schema_version = 3

[project]
name = "demo"

[build]
profile = "debug"

[toolchain]
cxx = "auto"

[targets.math]
type = "static"
sources = ["src/math.cpp"]
public_include_dirs = ["include"]

[[targets.math.dependencies]]
package = "glm"
scope = "public"

[targets.engine]
type = "static"
sources = ["src/engine.cpp"]

[[targets.engine.dependencies]]
target = "math"
scope = "public"

[[targets.engine.dependencies]]
package = "zlib"
scope = "private"

[targets.app]
type = "executable"
sources = ["src/main.cpp"]

[[targets.app.dependencies]]
target = "engine"
scope = "private"

[packages.glm]
provider = "vcpkg"
port = "glm"
triplet = "auto"

[packages.zlib]
provider = "vcpkg"
port = "zlib"
triplet = "auto"
```

不要写：

```toml
cxx_flags = "-Ifoo -Ibar"
link_flags = "-LC:/xxx -lz"
```

作为主模型。

Raw flag 只能是 escape hatch。

### 建立 `Usage` 对象

```go
type Usage struct {
	IncludeDirs []IncludeDir
	Defines     []Define

	LinkDirs    []string
	LinkItems   []LinkItem

	CompileOptions []Option
	LinkOptions    []Option

	RuntimeFiles []string
}

type LinkItem struct {
	Kind LinkItemKind
	Name string
	Path string
}
```

每个 Target 分成：

```go
type Target struct {
	Private   Usage
	Public    Usage
	Interface Usage

	Deps []Dependency
}
```

Dependency：

```go
type DependencyScope uint8

const (
	Private DependencyScope = iota
	Public
	Interface
)

type Dependency struct {
	Ref   DependencyRef
	Scope DependencyScope
}
```

语义与 CMake 熟悉的模型类似：

| scope | 当前 target 使用依赖 interface | 当前 target 对下游导出 |
|---|---:|---:|
| PRIVATE | 是 | 否 |
| PUBLIC | 是 | 是 |
| INTERFACE | 否 | 是 |

CMake 官方 `target_link_libraries` 文档对 PUBLIC/PRIVATE/INTERFACE 的基本传播语义就是这样设计的。citeturn9search11

不过我不建议声称自己的实现“完全兼容 CMake”，因为 CMake 的实际 usage requirements、generator expressions、imported targets、静态库 link interface 等细节远比这个基础模型复杂。

### 具体传播算法

假设：

```text
A ──private──> B
B ──public───> C
C:
    public include = c/include
```

计算 B：

```text
SelfCompile(B)
    += Interface(C)

ExportedInterface(B)
    += Interface(C)
```

计算 A：

```text
SelfCompile(A)
    += Interface(B)
    += Interface(C)
```

但：

```text
ExportedInterface(A)
```

不包含 B，因为：

```text
A -> B = PRIVATE
```

可以直接实现成两套闭包。

```go
func ComputeSelfUsage(
	target TargetID,
	g *Graph,
) (Usage, error)

func ComputeInterfaceUsage(
	target TargetID,
	g *Graph,
) (Usage, error)
```

伪代码：

```text
interface(T):
    T.public
    + T.interface

    for dependency D:
        if D.scope in {PUBLIC, INTERFACE}:
            + interface(D.target)

self(T):
    T.private
    + T.public

    for dependency D:
        if D.scope in {PRIVATE, PUBLIC}:
            + interface(D.target)
```

必须：

```text
stable dedup
```

而不能：

```text
sort all libraries
```

因为 include path 通常可以规范化去重，但 link item 的相对顺序在部分链接器场景中是语义的一部分。

### 链接传播不要完全复用 compile propagation

建议内部明确分成：

```go
type CompileUsage struct { ... }
type LinkUsage struct { ... }
```

尤其静态库：

```text
libB.a
  uses
libC.a
```

创建 `libB.a` 时并不会把 C 的对象自动“吸进 B”；最终 executable 的 link closure 仍需要知道这个 implementation dependency。

因此建议：

```go
type ResolvedTarget struct {
	CompileSelf      CompileUsage
	CompileInterface CompileUsage

	LinkSelf         LinkUsage
	LinkInterface    LinkUsage

	ImplementationLinkDeps []TargetID
}
```

这样你不需要为了模仿 CMake 的内部行为把所有东西都硬塞进 PUBLIC/PRIVATE/INTERFACE。

第一版建议直接**禁止 target graph 循环**：

```text
A static → B static → A static
```

报：

```text
E_DEP_CYCLE:
  target dependency cycle:
  A -> B -> A

hint:
  merge these libraries or refactor the dependency boundary
```

而不是第一版就实现 GNU linker group、MSVC 不同循环语义等。

### vcpkg 的正确抽象不是“安装目录扫描器”

vcpkg 官方的 common installed layout 包含：

```text
vcpkg_installed/
└── <triplet>/
    ├── include/
    ├── lib/
    │   ├── *.a / *.lib / *.so ...
    │   └── pkgconfig/
    ├── bin/
    ├── share/
    │   └── pkgconfig/
    └── debug/
        ├── lib/
        └── bin/
```

manifest 模式通常使用项目下的 `vcpkg_installed`，而 classic mode 有不同安装根；官方手工集成文档也明确给出了 release/debug 的 include/library/binary/pkg-config 布局。citeturn7view0

所以：

```go
type VcpkgLayout struct {
	Root    string
	Triplet string

	IncludeDir string

	ReleaseLibDir string
	DebugLibDir   string

	ReleaseBinDir string
	DebugBinDir   string

	PkgConfigDirs []string
}
```

是合理的。

但：

```go
func ResolvePackage(port string) []string {
    return glob(vcpkg/lib/*.lib)
}
```

是错误方向。

因为那个目录是整个 triplet 的聚合安装目录。

### 包解析建议使用 resolver chain

```go
type PackageResolver interface {
	Resolve(
		ctx context.Context,
		req PackageRequest,
	) (ResolvedPackage, error)
}

type ResolvedPackage struct {
	Name    string
	Version string

	Compile CompileUsage
	Link    LinkUsage

	RuntimeFiles []string

	Resolution ResolutionMethod
}

type ResolutionMethod string

const (
	ResolutionPkgConfig ResolutionMethod = "pkg-config"
	ResolutionAdapter   ResolutionMethod = "adapter"
	ResolutionManual    ResolutionMethod = "manual"
	ResolutionHeuristic ResolutionMethod = "heuristic"
)
```

推荐优先级：

```text
vcpkg install
      │
      ▼
Does package expose .pc?
      │
 yes ─┴─→ pkg-config resolver
      │ no
      ▼
Known port adapter?
      │
 yes ─┴─→ built-in metadata resolver
      │ no
      ▼
User override?
      │
 yes ─┴─→ manual metadata
      │ no
      ▼
Can conservative heuristic prove result?
      │
 yes ─┴─→ mark heuristic
      │ no
      ▼
E_PACKAGE_METADATA_UNSUPPORTED
```

vcpkg 官方布局中明确有 release/debug 的 pkg-config 目录，因此 `pkg-config` 是一个很适合你的中间层：它是结构化 link/compile metadata，但又不要求执行 CMake。citeturn7view0

### Header-only 不应通过名字维护名单

错误：

```go
headerOnlyPorts := map[string]bool{
    "glm": true,
    ...
}
```

更稳妥：

```text
resolver result:
    IncludeDirs != empty
    LinkItems == empty

→ Header-only from this build system's perspective
```

即：

```go
type ResolvedPackage struct {
	Compile ...
	Link ...
}
```

如果：

```go
len(pkg.Link.LinkItems) == 0
```

自然就是：

```text
compile usage only
```

不需要一个独立的“header-only package type”。

### `xxxConfig.cmake` 不要尝试静态解释

CMake Config mode 本质上就是查找和加载包提供的 Config 文件，并且这些包经常通过 imported targets 向消费者暴露 usage requirements。citeturn30view2

因此：

```text
FooConfig.cmake
```

不是简单：

```text
include=/xxx
lib=foo
```

的数据文件。

从系统设计角度推论，静态提取完整语义意味着你迟早需要处理：

```text
if()
include()
set()
find_dependency()
IMPORTED_LOCATION_DEBUG
IMPORTED_LOCATION_RELEASE
INTERFACE_INCLUDE_DIRECTORIES
INTERFACE_LINK_LIBRARIES
generator expressions
platform conditionals
feature conditionals
```

也就是逐渐实现一个 CMake interpreter。

**推荐明确拒绝这个方向。**

但不要因为存在：

```text
FooConfig.cmake
```

就直接拒绝包。

正确判断应是：

```text
这个包是否存在“你能解析的 metadata”
```

例如同时存在：

```text
FooConfig.cmake
foo.pc
```

那么 `.pc` 已经足够。

否则 TUI：

```text
┌ Package: foo ──────────────────────────────┐
│ Installed successfully.                   │
│                                           │
│ Build metadata cannot be resolved without │
│ the package's CMake configuration.        │
│                                           │
│ Available actions:                        │
│   > Add manual link metadata              │
│     Open package files                    │
│     Remove dependency                     │
└───────────────────────────────────────────┘
```

错误：

```text
E_PACKAGE_METADATA_UNSUPPORTED

foo was installed by vcpkg, but no supported build metadata
was found.

Detected:
  share/foo/FooConfig.cmake

Not detected:
  pkg-config metadata
  built-in forge adapter

Add an explicit override:

[packages.foo.override]
include_dirs = [...]
libraries = [...]
```

这是非常好的产品边界，而不是缺陷。

### triplet 不应由 `OS + arch` 唯一决定

vcpkg 的 triplet 不只是 architecture。官方 triplet variables 还描述 CRT linkage、library linkage、target platform、toolchain 等；当前文档列出了 `VCPKG_TARGET_ARCHITECTURE`、`VCPKG_CRT_LINKAGE`、`VCPKG_LIBRARY_LINKAGE`、`VCPKG_CMAKE_SYSTEM_NAME` 等。citeturn30view0

所以：

```go
func InferTriplet(os, arch string) string
```

抽象太弱。

应该：

```go
type TripletRequest struct {
	OS           string
	Arch         string
	Compiler     ToolchainKind
	CRTLinkage   Linkage
	LibLinkage   Linkage
}

type TripletCandidate struct {
	Name  string
	Score int
	Why   []string
}
```

TUI 逻辑：

```text
Target = Windows/x64
Toolchain = MSVC
Library linkage = dynamic
CRT = dynamic

→ query currently available vcpkg triplets
→ rank candidates
→ select x64-windows
→ persist actual name
```

对于：

```toml
triplet = "auto"
```

CI 语义应是：

```text
0 matches → error
1 match   → use
>1 match  → error
```

而不是悄悄选择第一个。

一旦 TUI 完成配置，推荐将：

```toml
triplet = "x64-windows"
```

真实写进配置，这样 CI 就不需要重新猜。

## C++ Modules 与增量生成策略

这是架构中最需要谨慎处理的一块。

### 不要建立统一 `.bmi` 假象

内部可以叫：

```go
type ModuleArtifact struct
```

但不要假设所有 compiler 都真正生成：

```text
foo.bmi
```

现实至少应区分：

```text
Clang → PCM
MSVC  → IFC
GCC   → CMI/GCM + module mapper/cache semantics
```

GCC 14 官方模块文档明确说其 modules support 仍然不完整，并说明编译 module interface 会额外产生 CMI，import graph 必须形成 DAG，importer 之前必须先生成所需 CMI。citeturn17view1

所以：

```go
type ModuleArtifact struct {
	LogicalName string
	Path        string
	Kind        ModuleArtifactKind
}
```

而不是：

```go
type BMI struct {
	Path string
}
```

### P1689 需要哪些字段

P1689R5 的顶层核心是：

```json
{
  "version": 1,
  "revision": 0,
  "rules": []
}
```

每个 rule 可以包含：

```json
{
  "primary-output": "...",
  "outputs": [],
  "provides": [],
  "requires": []
}
```

`provides` / `requires` 的关联核心字段是：

```json
"logical-name"
```

还可能包含：

```text
source-path
compiled-module-path
unique-on-source-path
lookup-method
is-interface
```

P1689 明确规定 `logical-name` 用于把提供方和需求方对应起来，从而确定 compilation rule 的执行顺序；`compiled-module-path` 本身是可选的，如果 scanner 没有提供，可以由 build system 选择。citeturn22view0

建议直接映射：

```go
type Document struct {
	Version  int    `json:"version"`
	Revision int    `json:"revision,omitempty"`
	Rules    []Rule `json:"rules"`
}

type Rule struct {
	PrimaryOutput string   `json:"primary-output,omitempty"`
	Outputs       []string `json:"outputs,omitempty"`
	Provides      []Module `json:"provides,omitempty"`
	Requires      []Module `json:"requires,omitempty"`
}

type Module struct {
	LogicalName        string `json:"logical-name"`
	SourcePath         string `json:"source-path,omitempty"`
	CompiledModulePath string `json:"compiled-module-path,omitempty"`

	UniqueOnSourcePath bool   `json:"unique-on-source-path,omitempty"`
	LookupMethod       string `json:"lookup-method,omitempty"`
	IsInterface        *bool  `json:"is-interface,omitempty"`
}
```

这里 `IsInterface` 用 pointer 是为了区分：

```text
absent → specification default
false
true
```

### 扫描图生成算法

假设扫描结果：

```text
math.cppm:
    provides math

geometry.cppm:
    provides geometry
    requires math

main.cpp:
    requires geometry
```

第一步建立 provider map：

```text
math     → math.cppm
geometry → geometry.cppm
```

第二步解析：

```text
geometry requires math
```

得到：

```text
math.cppm → geometry.cppm
```

再：

```text
main requires geometry
```

得到：

```text
geometry.cppm → main.cpp
```

最终：

```text
math → geometry → main
```

代码：

```go
type ModuleNode struct {
	TU       string
	Provides []ModuleKey
	Requires []ModuleKey
}

type ModuleKey struct {
	LogicalName string
	SourcePath  string
	BySource    bool
}

func BuildModuleGraph(
	scans []ScanResult,
) (*ModuleGraph, error)
```

必须检测：

```text
duplicate provider
missing provider
cycle
```

错误例如：

```text
E_MODULE_DUPLICATE_PROVIDER

module "math" is provided by:
  src/math.cppm
  src/math2.cppm
```

P1689 对 header unit 还特别定义了 `unique-on-source-path`，因为相同 header 可以通过不同文本名称被 import；这种情况下 provider/consumer 关联可能必须基于规范化 `source-path`，不能只看文本 `logical-name`。citeturn22view0

### Ninja 不需要你“手动按拓扑顺序输出”

拓扑排序主要用途是：

```text
validate cycle
explain graph
stable diagnostics
```

Ninja 自己会根据依赖边安排执行。

例如抽象上：

```ninja
build bmi/math.pcm obj/math.o: clang_module src/math.cppm

build bmi/geometry.pcm obj/geometry.o: clang_module src/geometry.cppm | bmi/math.pcm

build obj/main.o: clang_cxx src/main.cpp | bmi/geometry.pcm
```

这里：

```text
bmi/math.pcm
```

应该是 **implicit input**，而不是 order-only dependency：

```ninja
| bmi/math.pcm
```

原因是消费者不仅需要顺序保证，也应该在 module artifact 发生变化时被视为受影响。

### Clang 扫描

Clang 官方文档已经支持：

```text
clang-scan-deps -format=p1689
```

并给出了 compilation database 与直接 command line 两种模式。官方文档中的单命令模式形态是：

```text
clang-scan-deps -format=p1689 -- \
  clang++ ... source.cppm -c ...
```

也支持以 compilation database 为输入。citeturn14search0turn14search3

你的实现建议优先做**per-TU scan**：

```text
clang-scan-deps
  -format=p1689
  --
  clang++
  -std=c++20
  ...
  -c src/foo.cppm
  -o out/foo.o
```

原因不是效率最好，而是缓存最简单：

```text
source TU
   ↕
one cache entry
```

后期可以再加入 batch compilation database scanner。

Clang module backend 可以概念上生成：

```text
clang++ -std=c++20 --precompile foo.cppm -o foo.pcm

clang++ -std=c++20 \
  -fmodule-file=foo=foo.pcm \
  -c consumer.cpp \
  -o consumer.o
```

但这部分务必放在：

```text
modules/backends/clang
```

而不是硬编码进 Ninja emitter。

### MSVC 扫描

当前 MSVC 文档列出的模块相关命令包括：

```text
/scanDependencies
/interface
/ifcOutput
/reference
```

其中 `/scanDependencies` 用于产生标准 C++ JSON 形式的 module/header-unit dependency 信息；`/ifcOutput` 控制 IFC 输出，`/reference` 引用 named module IFC。citeturn15search0turn15search6

因此适配器可以使用：

```text
cl.exe
  /nologo
  /std:c++20
  /c foo.ixx
  /Fofoo.obj
  /scanDependencies foo.json
```

随后实际 module compile：

```text
cl.exe
  /nologo
  /std:c++20
  /interface
  /c foo.ixx
  /Fofoo.obj
  /ifcOutput foo.ifc
```

消费者：

```text
cl.exe
  ...
  /reference foo=foo.ifc
  /c consumer.cpp
  /Foconsumer.obj
```

具体参数组合仍应该由 capability probe 覆盖，而不是靠：

```go
if msvcVersion >= "17.4"
```

决定可用。

### GCC 建议晚于 Clang/MSVC

GCC 14 官方手册明确仍把 C++ Modules 支持描述为“不完整”，且模块需要 `-fmodules-ts`，CMI 的产生和查找方式与 Clang/MSVC 不同。citeturn17view1

P1689 扫描的典型 GCC 集成形态会涉及：

```text
-fmodules-ts
-fdeps-format=p1689r5
-fdeps-file=...
```

以及 dependency target/module mapper 相关设置。

因此我建议不要在 v1 对用户宣称：

```text
Clang 16+
GCC 14+
MSVC 17.4+
```

等价支持。

改成：

```text
C++ Modules

Clang     ✓ verified
MSVC      ✓ verified
GCC       experimental
AppleClang unsupported by detected capabilities
```

关键是 **feature probe 而不是 vendor/version 表**：

```go
type ModuleProbe struct {
	P1689Scan         bool
	NamedModules      bool
	Partitions        bool
	HeaderUnits       bool
	ExplicitArtifact  bool
}
```

工具启动后实际运行一个极小 probe。

这还能自然解决“Apple Clang 永远不支持”这样的时效性问题：不是硬编码厂商黑名单，而是：

```text
clang-scan-deps -format=p1689
+
minimal compile
```

能过就启用，不能过就关闭。

### 静态生成路线最大的边界：generated module source

P1689 的设计背景本来就考虑了构建过程中依赖信息可能变化，以及 generated source 的存在；标准论文强调模块依赖必须在 compilation rule 运行之前被发现。citeturn22view0

你的模式：

```text
scan
↓
generate build.ninja
↓
ninja
```

意味着：

```text
foo.cppm
```

必须在 Ninja 开始之前就已经存在。

因此第一版建议明确：

> **Module interface/source 必须是 generation-time existing source。构建过程中生成的 `.cppm/.ixx` 暂不支持。**

不要偷偷支持 80%。

以后想支持，则必须引入 phase zero：

```text
pre-generation generators
        ↓
generated module sources
        ↓
P1689 scan
        ↓
build.ninja
```

这已经接近二阶段构建系统，应该后做。

### Modules 的缓存必须同时保存普通 header dependencies

这里有一个非常容易踩坑的地方。

P1689R5 在 R4 中明确移除了普通 `inputs/depends` 信息，因为 module dependency format 不打算替代 GCC `-M` 一类的普通 header depfile。citeturn22view0

所以扫描缓存不能只做：

```text
hash(foo.cpp)
```

例如：

```cpp
// feature.hpp
#define USE_GRAPHICS_MODULE 1
```

然后：

```cpp
#include "feature.hpp"

#if USE_GRAPHICS_MODULE
import graphics;
#endif
```

如果只看 TU 内容：

```text
foo.cpp unchanged
```

但 header 改动已经改变 module graph。

正确 cache entry：

```go
type ModuleScanCacheEntry struct {
	Key ScanKey

	ResultHash string
	Result     p1689.Document

	HeaderDeps []FileStamp
}
```

ScanKey：

```go
type ScanKey struct {
	SourceHash          string
	CompilerFingerprint string
	CompileSignature    string
}
```

其中 `CompileSignature` 至少包括：

```text
language standard
-D defines
-U undefines
include search paths
system include paths
module mode
target triple
toolchain environment affecting preprocessing
```

scanner 同时请求正常 depfile：

```text
foo.scan.d
```

缓存命中逻辑：

```text
source unchanged?
flags unchanged?
compiler unchanged?
all scan-time header dependencies unchanged?

yes → reuse P1689
no  → rescan TU
```

这样修改普通函数体：

```text
foo.cpp
```

只会 rescan `foo.cpp`。

若扫描结果 hash 相同：

```text
module graph unchanged
```

就无需重写 `build.ninja`。

### 整体增量 generation fingerprint

建议构建目录：

```text
build/debug/
├── build.ninja
├── obj/
├── bin/
└── .forge/
    ├── generation.json
    ├── toolchains.json
    ├── sources.json
    ├── packages.json
    └── modules/
        ├── index.json
        ├── xxx.p1689.json
        └── xxx.scan.d
```

`GenerationKey`：

```text
hash(
    generator_version,
    schema_version,
    normalized_config,
    profile,
    resolved_source_list,
    toolchain_fingerprint,
    package_resolution_fingerprint,
    sdk_fingerprint,
    module_graph_hash
)
```

这里不要包含所有普通 C++ source 内容。

否则：

```text
change one function body
```

都会导致：

```text
regenerate build.ninja
```

真正应触发 manifest regeneration 的是：

```text
project config changed
source set changed
toolchain changed
profile/options changed
package resolution changed
SDK changed
module dependency graph changed
```

而普通 `.cpp` 内容变化交给 Ninja。

Ninja 自己也会把 command line 变化视为输出需要重新构建的因素，因此只要 manifest 中 command 真正变化，Ninja 就会正确重新执行相应 edge。citeturn30view1

### Source glob 需要特殊处理

如果配置：

```toml
sources = ["src/**/*.cpp"]
```

那么 config hash 不变：

```text
src/new.cpp added
```

仍应触发 regenerate。

所以即使其他东西都缓存，也应该快速做：

```text
glob
→ normalize
→ sort
→ hash path list
```

而不是只 hash TOML。

如果用户只使用显式：

```toml
sources = [
    "src/a.cpp",
    "src/b.cpp",
]
```

则无需扫描目录。

### Toolchain fingerprint

建议：

```go
type Fingerprint struct {
	Executable string
	Version    string
	Target     string

	FileSize  int64
	FileMTime int64

	EnvironmentHash string
}
```

MSVC 再加：

```text
VS instance
toolset
Windows SDK
INCLUDE
LIB
```

CUDA：

```text
nvcc fingerprint
+
host compiler fingerprint
+
CUDA toolkit root
```

通常无需每次 SHA-256 整个 `cl.exe` 或 `g++`，否则检测本身可能比 generation 还贵。

### manifest 不变时不要重写

一定要：

```text
render new bytes
      ↓
compare old bytes
      ↓
identical?
  yes → leave file untouched
  no  → atomic replace
```

否则仅 wrapper 启动一次就修改 `build.ninja` mtime，会导致各种不必要的 regeneration 行为。

## TUI、配置演进、CI 与错误反馈

### 配置是 source of truth，TUI 状态不是

建议严格分离：

```text
project.toml
```

存：

```text
what the project means
```

而：

```text
build/.forge/ui-state.json
```

最多存：

```text
last selected tab
expanded groups
last search query
scroll position
```

不要产生：

```toml
last_cursor = 7
last_menu = "msvc"
```

这种污染项目语义的字段。

### Schema 版本迁移

配置顶部：

```toml
schema_version = 3
```

不要用：

```text
absence/presence of field X
```

猜版本。

代码：

```go
type Migrator interface {
	From() int
	To() int
	Migrate(any) (any, error)
}
```

或者更直接的版本 struct：

```text
bytes
 ↓
detect schema_version
 ↓
schema/v1.Document
 ↓
v1 → v2
 ↓
schema/v2.Document
 ↓
v2 → v3
 ↓
config.Current
```

迁移规则：

```text
v1 -> v2 -> v3
```

不要：

```text
v1 -> current
v2 -> current
```

这样每个迁移只处理一个版本差异。

对于：

```text
schema_version = 6
```

而当前程序只支持 5：

```text
E_CONFIG_NEWER_SCHEMA

project requires schema 6,
but this executable supports up to schema 5.

Upgrade the build tool.
```

绝对不要“尽量读”。

### TOML round-trip 是一个需要早测的风险

你选择 `BurntSushi/toml` 本身没问题，但这里应该尽早写 fixture 测试验证：

```text
comments
ordering
unknown fields
formatting
```

在：

```text
decode → TUI edit → encode
```

之后是否符合你的 UX 要求。

如果你的产品承诺：

> “用户手写的 TOML 是一等公民”

那么 comment preservation 很重要。

若所选 codec 无法满足，只有两个合理选择：

```text
A. 明确声明工具拥有配置格式，保存时 canonical rewrite
```

或者：

```text
B. 后续换成支持 AST/document round-trip 的 TOML 编辑层
```

不要一边允许手写大量注释，一边 TUI 保存一次全部消失。

### 动态 TUI 选项应基于 Capability Graph

不要：

```go
if selected == "msvc" {
    showX64 = true
}
```

散落在 view。

建立：

```go
type EnvironmentSnapshot struct {
	Toolchains []Candidate
	SDKs       []SDKCandidate
	Vcpkg      *VcpkgInfo
}

type ConfigurationDomain struct {
	CompilerOptions []ToolchainOption
	Architectures    []ArchOption
	CUDAVersions     []CUDAOption
	HostCompilers    []HostCompilerOption
}
```

用户选择：

```text
MSVC
```

后 reducer：

```text
state selection changed
        ↓
RecomputeConfigurationDomain()
        ↓
only valid architectures shown
```

选择：

```text
CUDA 12.4
```

后：

```text
all detected host compilers
        ↓
static compatibility filter
        ↓
actual nvcc probe
        ↓
verified candidates
```

展示建议：

```text
Host compiler

● MSVC 14.38    ✓ verified
○ MSVC 14.42    ! not verified for this CUDA toolkit
○ Clang 18      ✗ probe failed
```

NVCC 官方本身明确区分支持与 unsupported-host override，因此这种展示比单纯版本 hardcode 更安全。citeturn28view0

### CI 必须 strict，不要“聪明默认”

非 TTY 情况我建议规则非常简单：

```text
implicit guess → 禁止
explicit auto  → 允许
```

例如配置没有：

```toml
[toolchain]
```

那么 CI：

```text
error
```

而不是：

```text
PATH 第一个 g++
```

因为本地开发机可能是：

```text
Clang 20
```

CI 是：

```text
GCC 15
```

最终产生“同一配置不同语义”。

若用户明确：

```toml
[toolchain]
cxx = "auto"
```

则：

```text
exactly one suitable candidate → use it
zero candidates               → error
multiple equivalent candidates → error
```

可以再提供显式策略：

```toml
cxx = "clang"
version = ">=18"
```

这种“explicit auto”仍是确定的用户意图。

### 错误模型要可机器处理

不要所有东西：

```go
return fmt.Errorf("failed: %w", err)
```

建议：

```go
type Stage string

const (
	StageConfig     Stage = "config"
	StageToolchain  Stage = "toolchain"
	StageDependency Stage = "dependency"
	StageScan       Stage = "module-scan"
	StageGenerate   Stage = "generate"
	StageBuild      Stage = "build"
)

type Error struct {
	Code  string
	Stage Stage

	Summary string
	Detail  string
	Hints   []string

	Command *Command
	LogPath string

	Cause error
}
```

例如：

```text
E_TOOLCHAIN_CUDA_HOST_INCOMPATIBLE
E_PACKAGE_METADATA_UNSUPPORTED
E_MODULE_SCAN_FAILED
E_MODULE_CYCLE
E_CONFIG_MISSING_REQUIRED
E_NINJA_FAILED
```

普通 terminal：

```text
error[E_MODULE_SCAN_FAILED]: failed to scan src/net.cppm

toolchain:
  clang 20.1.3

command:
  clang-scan-deps ...

compiler output:
  ...

hint:
  run `forge diagnose modules`
```

TUI：

```text
┌ Build failed ───────────────────────────────────────────┐
│ module-scan                                             │
│                                                        │
│ src/net.cppm                                            │
│ clang-scan-deps exited with code 1                     │
│                                                        │
│ › Show command                                          │
│   Open full log                                         │
│   Copy diagnostic                                      │
└────────────────────────────────────────────────────────┘
```

### Ninja 实时输出存在一个物理边界

这一点要提前接受。

Ninja 官方行为是：并行命令的输出通常被缓存，避免多个 command 的输出互相交错，并在 command 失败时把失败输出连同命令集中打印。citeturn30view1

因此通过官方 Ninja，你能很好地得到：

```text
real-time build progress
```

但不能保证：

```text
每个正在运行的 compiler 的 stdout 每一行实时到达 TUI
```

除非改变规则的 console 使用方式，而那会改变并行输出/调度体验。

所以 UI 设计应该是：

```text
实时：
  progress
  active edge descriptions

边完成/失败时：
  buffered compiler output
```

这和官方 Ninja 的行为吻合。

### 用 `NINJA_STATUS` 做稳定进度协议

不要解析默认：

```text
[37/128]
```

因为 Ninja 自己支持 `NINJA_STATUS`，并提供：

```text
%s started
%t total
%p percentage
%r running
%u remaining
%f finished
%e elapsed
%E ETA
...
```

等 placeholder。citeturn30view1

启动 Ninja 时设置：

```text
NINJA_STATUS=@@FORGE:%f:%t:%p:%r@@
```

例如：

```text
@@FORGE:37:128:28:12@@ CXX foo.cpp
```

parser：

```go
type Progress struct {
	Finished int
	Total    int
	Percent  int
	Running  int
}
```

其他内容原样进入 log pane。

这样不会依赖 Ninja 默认 status formatting。

### TUI 运行 Ninja

建议：

```go
type Runner interface {
	Run(
		ctx context.Context,
		req BuildRequest,
		sink EventSink,
	) BuildResult
}

type EventSink interface {
	Progress(Progress)
	Output(OutputEvent)
}

type OutputEvent struct {
	Stream Stream
	Text   string
}
```

Bubble Tea 侧：

```text
tea.Cmd
  ↓
process event
  ↓
tea.Msg
  ↓
Update()
```

保存：

```text
bounded ring buffer
```

而不是让 30 万行编译日志一直存在 Bubble Tea Model 中。

非 TTY：

```text
stdout → stdout
stderr → stderr
```

不做 spinner、不做 ANSI UI。

## 测试、实现顺序、风险与产品定位

### 单元测试与 integration test 的边界

大量核心逻辑都应该做到**完全不需要机器上真的存在编译器**。

| 模块 | 测试方式 |
|---|---|
| config migrations | golden fixtures |
| TOML validation | table tests |
| target graph | pure unit |
| PUBLIC/PRIVATE propagation | table tests |
| link closure | graph fixtures |
| toolchain detection | fake FS + fake Executor |
| GCC/MSVC command lowering | golden command tests |
| CUDA composition | fake NVCC + fake host |
| P1689 decoder | JSON fixtures |
| module provider graph | pure unit |
| module cycle detection | pure unit |
| scan cache | fake timestamps/hash |
| Ninja AST/emitter | snapshot/golden |
| Ninja escaping | property/fuzz tests |
| vcpkg layout | fake directory fixture |
| triplet selector | table tests |
| error rendering | golden |
| TUI reducer | update/message tests |

特别推荐给 process execution 一个非常小的接口：

```go
type Executor interface {
	Run(
		ctx context.Context,
		cmd Command,
	) (Result, error)
}
```

Toolchain detector 单测就可以：

```text
when `clang++ --version`
return fixture output
```

而不是把：

```text
exec.Command
```

散落到所有 adapter。

### Ninja manifest snapshot 测试

例如 fixture：

```text
testdata/projects/static-chain/
```

输入：

```text
app
 ↓
libA
 ↓
libB
```

expected：

```text
testdata/manifests/static-chain-linux-clang.ninja
testdata/manifests/static-chain-windows-msvc.ninja
```

测试：

```go
got := Generate(project)
golden.Assert(t, got)
```

同时真正运行：

```text
ninja -t targets
```

验证 generated manifest 能被官方 Ninja parser 接受。

这很重要，因为 Ninja escaping 尤其有：

```text
space
:
$
Windows drive
backslash
response file
```

等边角情况。

### 端到端 fixture 项目

建议维护以下真实工程：

```text
fixtures/
├── hello-c/
├── hello-cpp/
├── target-propagation/
├── static-chain/
├── shared-lib/
├── vcpkg-header-only/
├── vcpkg-linked/
├── cuda-basic/
├── cuda-rdc/
├── vulkan-shader/
├── modules-linear/
├── modules-diamond/
├── modules-partition/
└── modules-cycle-error/
```

`modules-diamond`：

```text
       core
      /    \
     A      B
      \    /
       app
```

这是检测 module edge 传播和重复引用最有价值的 fixture 之一。

### CI 编译器矩阵

基础 CI：

```text
Linux:
  GCC
  Clang

Windows:
  MSVC
  MinGW

macOS:
  Clang
```

另外：

```text
CUDA
```

建议单独 CI job，因为 CUDA Toolkit/runner 环境成本更大。

Modules 更要按真实 capability matrix 分开：

```text
modules-clang
modules-msvc
modules-gcc-experimental
```

不要一个：

```text
modules=true
```

测试把三个 compiler 当成相同实现。

### 实现优先级与复杂度

| 模块 | 复杂度 | 优先级 | 原因 |
|---|---:|---:|---|
| `model` + `plan` | M | P0 | 所有后续基础 |
| config/schema | M | P0 | 项目 source of truth |
| `execx` / FS abstraction | S | P0 | 后续测试基础 |
| target dependency graph | M/L | P0 | 核心语义 |
| GCC/Clang | M | P0 | 最快形成可用产品 |
| MSVC | L | P0 | Windows 核心 |
| MinGW | M | P0 | Windows GNU 路线 |
| Ninja emitter | M/L | P0 | 产品核心 |
| Ninja runner | M | P0 | 完成 MVP |
| strict non-interactive mode | S/M | P0 | CI 必须成立 |
| TUI | M/L | P1 | 差异化 UX |
| generation fingerprint | M | P1 | wrapper overhead |
| Vulkan SDK/shader | M | P1 | 独立且低风险 |
| vcpkg install/search | M | P1 | UX |
| vcpkg metadata resolver | L/XL | P1 | 最大 package 风险 |
| CUDA composite toolchain | L | P2 | host/device 特殊性 |
| C++ Modules Clang | XL | P2/P3 | 高复杂度 |
| C++ Modules MSVC | XL | P3 | backend 差异 |
| C++ Modules GCC | XL | P3 | 当前差异和 incomplete support |
| Fortran | L/XL | P3 | 非核心目标 |

这里最重要的是：

> **不要在有一个可靠 C/C++ MVP 之前实现 Modules。**

否则你会在还没稳定 `Target → Action → Ninja` 模型时，被 compiler-specific module semantics 迫使核心架构反复重构。

### 推荐开发阶段

第一阶段：

```text
TOML
 ↓
C/C++ targets
 ↓
GCC/Clang/MSVC/MinGW
 ↓
PUBLIC/PRIVATE dependency propagation
 ↓
Ninja
 ↓
CI strict mode
```

这时产品已经真的可用。

第二阶段：

```text
TUI
vcpkg search/install
supported-package resolver
Vulkan shader
generation caching
diagnostics
```

第三阶段：

```text
CUDA composite toolchain
RDC/device link
```

第四阶段：

```text
P1689 infrastructure
Clang Modules
MSVC Modules
GCC experimental
```

最后：

```text
Fortran
advanced package metadata
```

### 我明确建议不做的功能

**不实现 CMake Config interpreter。** CMake Config mode 本身就是包生态的重要编程接口，试图静态解释它会把你的项目拖进实现 CMake 语言和 imported-target semantics 的方向。citeturn30view2

**不实现 `find_package` 通用兼容层。** 支持你能明确解析的 package metadata，其余通过 manual override。

**不实现 scheduler。** Ninja 的核心工作就是从依赖图调度并执行 build edges，而且强调快速增量构建；重复实现没有产品价值。citeturn30view1

**不实现自己的 file timestamp incremental engine。** 只负责 generation/scanning cache，编译级增量交给 Ninja。

**不实现 install/export/CPack 第一版。** 你的目标用户不是提供成熟 binary/package SDK 的大型库项目。

**不提供任意 Go/Lua/Python build scripting API 第一版。** 一旦允许任意程序逻辑，配置模型、缓存 key、TUI round-trip、静态分析都会复杂一个数量级。

**不支持 generated C++ module sources 第一版。** 这会迫使生成系统出现 build-before-build 的第二阶段。

**不承诺所有 vcpkg ports 都能自动链接。** 这是非常重要的产品诚信边界。

### 最大的后期重构风险

风险最高的不是 Ninja，而是以下几项。

| 风险 | 等级 | 缓解方式 |
|---|---|---|
| vcpkg metadata 不够结构化 | 极高 | resolver chain + manual override |
| C++ compiler module 模型差异 | 极高 | 独立 ModuleBackend |
| BuildPlan 设计过度 Ninja 化 | 高 | Action/Artifact IR |
| CUDA 被当普通 compiler | 高 | composite toolchain |
| MSVC env 成为全局状态 | 高 | Candidate-owned Env |
| TOML rewrite 丢失 UX 信息 | 中高 | 早期 round-trip fixture |
| path/escaping/rspfile | 高 | 单独 `ninja/escape` + fuzz |
| static library propagation | 高 | compile/link closure 分离 |
| config `auto` 产生 CI 漂移 | 中高 | strict deterministic auto |
| generated module sources | 高 | v1 明确拒绝 |
| scope creep 成 CMake 替代品 | 极高 | 保持明确 non-goals |

其中最值得提前投资的是：

```text
BuildPlan IR
ModuleBackend
ResolvedPackage
Usage
```

这四个抽象正确，后面绝大多数功能只是 adapter。

### 与 CMake 的定位

CMake 已有成熟 target usage requirement 模型，`target_link_libraries` 能以 PUBLIC/PRIVATE/INTERFACE 描述并传播依赖；Config package 生态也依赖 `find_package()` 和 imported targets。citeturn9search11turn30view2

因此以下场景用户仍应优先 CMake：

```text
大型多仓库工程
复杂 install/export
需要发布 CMake package
广泛第三方 find_package 生态
复杂 cross-compilation/toolchain files
大型 IDE/企业工作流
高度复杂 generator expressions
成熟 C++ Modules + package integration
```

你的工具更合适：

```text
个人 / 小团队
几十到几百个源文件
C/C++/CUDA/Vulkan
少量明确外部依赖
希望不学习构建 DSL
希望工具链可视化选择
希望生成的 command / Ninja 完全可检查
CI 配置简单
```

关键不是：

> “比 CMake 更强。”

而是：

> **让不需要 CMake 90% 功能的用户不用承担 CMake 90% 的认知模型。**

### 与 Xmake 的定位

Xmake 官方定位本身就是一个跨平台、Lua 驱动的 build utility，并且拥有自己的 package/integration 生态，其能力范围明显比你计划的工具更宽。citeturn12search0turn12search3

你的真正区别应该是：

```text
Xmake
  high-level programmable build system
  Lua
  broader package/build ecosystem

你的工具
  declarative TOML
  intentionally non-programmable
  TUI-first configuration
  official Ninja is execution engine
  inspectable static build plan
```

因此不要和 Xmake 比：

```text
谁支持的功能数量更多
```

你大概率输。

应该比：

```text
第一次构建需要知道多少概念
配置一个 MSVC + CUDA project 需要编辑多少代码
添加一个简单依赖需要多少操作
CI 是否需要学习另一套语言
```

### 与 Meson 的定位

Meson 已经拥有较高级的 dependency abstraction；官方文档中的 dependency object 可以携带 include/compile/link 等使用信息，并作为项目内部/外部依赖传播。citeturn30view3

所以 Meson 与你的工具在“声明式、避免大量 CMake boilerplate”这个目标上比较接近。

差别应放在：

```text
Meson:
  build DSL
  high-level build semantics
  mature dependency abstraction

你的工具:
  UI generates/edit TOML
  static BuildPlan
  deliberately narrower compiler/package semantics
  toolchain discovery is first-class UX
  generated Ninja is explicit product artifact
```

也就是说，你不是：

> “更小的 Meson。”

而是：

> **用交互式配置体验替代一部分 build DSL 学习成本的 Ninja generator。**

### 与 cpx 的定位

我能定位到的 `cpx` 项目将自身描述为 **“C++ Package Exchange - Cargo Like Package Manager & Build System Using Modules”**，核心卖点显然偏向 Cargo-like package/build experience 和 C++ Modules。fileciteturn2file0L2-L2

它的 README 当前公开 getting-started 也主要给出 Debian 系安装路径，并表示其他系统仍欢迎贡献，因此至少从当前公开材料看，它与这里设计的“Windows/MSVC/CUDA/Vulkan/toolchain discovery + TUI + Ninja meta-generator”并不是完全同一产品路线。fileciteturn2file0L2-L2

你的差异化更应该是：

```text
cpx:
    Cargo-like C++ package/build workflow
    modules-centered

你的工具:
    toolchain-centered
    TUI-centered
    CUDA/Vulkan-centered
    Ninja-generator-centered
    vcpkg as transparent backend
```

而不是跟它竞争“谁更像 Cargo”。

### TUI 的价值必须量化

“TUI 很舒服”不是产品指标。

建议从第一版开始测这些指标：

| 指标 | 建议目标 |
|---|---|
| 新 C++ 项目从空目录到第一次 build | ≤ 8 个用户决策 |
| 选择 compiler/toolchain | 无需手写路径的常见安装覆盖率 > 90% |
| 添加受支持 vcpkg package | 搜索 → 选择 → 保存，不手写 flags |
| CI 配置 | 同一 TOML，无交互补充 |
| warm wrapper overhead | ≤ 200 ms 作为 1k-source 项目的长期目标 |
| unchanged manifest rewrites | 0 |
| module scan cache hit | warm build > 95% 目标 |
| error diagnostics | 100% 有 stage + stable error code |
| external-command failure | 100% 可查看实际 command |
| unsupported package | 在 build 前发现，而不是 linker 阶段发现 |

这里的数值应该视为**产品 SLO/研发目标**，不是当前性能承诺。

尤其值得跟 CMake/Meson/Xmake 做一个 5～10 人 usability test：

```text
任务：
  create executable
  add static library
  make it PUBLIC
  select compiler
  add zlib
  build Debug
  switch compiler
```

记录：

```text
time-to-first-success
documentation lookups
syntax errors
manual file edits
number of failed builds
```

TUI 真正的商业/产品价值不是它“漂亮”，而是：

```text
减少 invalid configuration states
+
把 environment discovery 变成 visible choices
+
把错误提前到 configuration phase
```

### 最终推荐的架构形态

整个系统最终最好能被理解成下面这张图：

```text
                   ┌─────────────────────────┐
                   │      Presentation       │
                   │ CLI / TUI / CI output   │
                   └────────────┬────────────┘
                                │
                                ▼
                   ┌─────────────────────────┐
                   │       Application       │
                   │ configure/generate/build│
                   └────────────┬────────────┘
                                │
             ┌──────────────────┼────────────────────┐
             │                  │                    │
             ▼                  ▼                    ▼
      ┌────────────┐    ┌──────────────┐      ┌────────────┐
      │ Toolchains │    │ Dependencies │      │    SDKs    │
      │ GCC/MSVC   │    │    vcpkg     │      │   Vulkan   │
      │ Clang/CUDA │    │ pkg-config   │      │            │
      └─────┬──────┘    └──────┬───────┘      └──────┬─────┘
            │                  │                     │
            └──────────────────┼─────────────────────┘
                               ▼
                   ┌─────────────────────────┐
                   │      Domain Model       │
                   │ Target / Usage / Graph  │
                   └────────────┬────────────┘
                                │
                     modules?   │
                       ┌────────┘
                       ▼
              ┌───────────────────────┐
              │ P1689 scan + cache    │
              │ provider/require graph│
              └───────────┬───────────┘
                          │
                          ▼
                   ┌───────────────┐
                   │   BuildPlan   │
                   │ Action graph  │
                   └───────┬───────┘
                           │
                           ▼
                   ┌───────────────┐
                   │ Ninja Lowerer │
                   └───────┬───────┘
                           │
                           ▼
                      build.ninja
                           │
                           ▼
                   ┌───────────────┐
                   │Official Ninja │
                   └───────┬───────┘
                           │
                           ▼
                    progress/result
```

这套设计最重要的思想是：

**Toolchain 只负责“如何把语义编译成命令”；Dependency Resolver 只负责“库向消费者提供什么”；Module Scanner 只负责“翻译单元之间有什么模块边”；BuildPlan 只负责“哪些 artifact 由哪些 action 产生”；Ninja emitter 最后才负责“怎样把这个图写成 Ninja”。**

Ninja 自身正是为“由更聪明的独立程序提前完成这些决策，然后生成精确输入”而设计的。citeturn30view1

如果坚持这个边界，你得到的不会是“另一个迷你 CMake”，而会是一个更明确的产品：**针对中小型原生项目，把工具链发现、依赖关系和构建配置中的高认知成本部分前移到一个可视化、可验证、可重复的配置阶段，再把真正擅长增量调度的工作完整交还给 Ninja。**