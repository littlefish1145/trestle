<p align="center">
  <img src="assets/trestle-banner.png" alt="Trestle" width="100%">
</p>

<p align="center">
  面向 C、C++、CUDA 与 Vulkan 项目的轻量级构建工作台
</p>

<p align="center">
  <a href="README_EN.md">English</a> · <a href="LICENSE">MIT License</a>
</p>

# Trestle

Trestle 把编译器发现、依赖解析、项目导入、Ninja 构建、测试和发布集中到一个 CLI 与 TUI 中。它以 `trestle.toml` 为项目事实来源，生成可检查的 `build.ninja`、`compile_commands.json` 和命令元数据，不隐藏真实的编译与链接过程。

## 主要能力

- 支持 C17、C++20，以及静态库、动态库、可执行文件、测试和 Vulkan Shader 目标。
- 自动发现 MSVC、Clang、clang-cl、GCC、MinGW、CUDA、Vulkan SDK 和 WSL 编译器。
- 使用 clang-cl 时自动查找并加载匹配的 Visual Studio ABI 环境；支持显式 `vcvars64.bat`、工具路径反推和 `vswhere`。
- 按目标构建：`trestle build app` 不会让无关 GUI、工具或测试目标阻塞主目标。
- 跟踪 C/C++ include 依赖，源文件或 wrapper include 变化会触发正确的增量重编译。
- 集成 vcpkg 在线搜索、安装、传递依赖、triplet、CRT/linkage、pkg-config 和运行时 DLL 部署。
- 原生导入 CMake File API 与 Xmake 项目。
- 测试支持单个 job、分组、源文件归属和全部运行。
- Release 页面/命令支持内置或自定义优化预设，并将目标及运行时依赖打包为 ZIP。
- 为 VS Code/clangd 生成 `compile_commands.json`，并生成 `.trestle/commands.json`。
- 构建诊断按目标和编译/链接阶段聚合，汇总成功、失败和跳过的目标。

## 环境要求

- Go 1.26 或更高版本（从源码构建 Trestle）
- Ninja
- 至少一个受支持的 C/C++ 编译器
- CMake（CMake 导入、导出符号和运行时文件部署需要）
- 可选：vcpkg、Xmake、CUDA Toolkit、Vulkan SDK、WSL

Windows 使用 clang-cl 时无需手动打开 Developer Command Prompt；Trestle 会自动选择有效的 Visual Studio x64 ABI 环境。

## 构建与安装

```powershell
go build -o trestle.exe ./cmd/trestle
```

或安装到 Go bin 目录：

```powershell
go install ./cmd/trestle
```

也可以从 GitHub Releases 下载自动生成的 Alpha 桌面安装包。每次 `main` 更新都会发布 Windows、Linux、macOS 的 x64 与 ARM64 版本：

- Windows：解压 ZIP，在包目录执行 `powershell -ExecutionPolicy Bypass -File .\install.ps1`。
- Linux/macOS：解压 `tar.gz`，在包目录执行 `./install.sh`。
- 安装器会复制完整的 `bin` 目录并把它加入用户 `PATH`；在 GitHub Actions 中会写入 `GITHUB_PATH`，供后续步骤直接调用 `trestle`。
- 每个 Alpha 预发布都包含提交记录、构建日志、包清单、SHA-256 校验和；完整 Actions 日志会在主流水线结束后自动附加。

安装完成后可确认构建身份：

```powershell
trestle version
```

开发验证：

```powershell
go test ./...
go vet ./...
```

## 快速开始

首次使用建议阅读 [从零构建带 fmt 依赖的 C++ 项目](docs/tutorial.zh-CN.md)，涵盖 Windows、Linux、macOS、WSL、完整日志及故障恢复；可运行示例见 [examples/hello-fmt](examples/hello-fmt/)。

```powershell
trestle init -C hello -name hello
cd hello
trestle configure -toolchain clang-cl
trestle build app
```

在项目目录直接运行 `trestle` 会打开 TUI；也可使用：

```powershell
trestle build --ui
```

## 常用命令

```text
trestle                         打开项目 TUI
trestle init                    创建项目
trestle configure               配置编译器、环境、CUDA、WSL 和预设
trestle analyze                 分析现有源码
trestle build [target ...]      构建指定目标
trestle build --all             构建全部目标
trestle test --job NAME         运行一个测试 job
trestle test --group NAME       运行一个测试组
trestle test --file FILE        运行拥有该源文件的测试
trestle test --all              运行全部测试
trestle import                  自动选择 Xmake 或 CMake 导入
trestle import-xmake            导入 Xmake
trestle release                 构建并生成 ZIP 发布包
trestle vcpkg --search QUERY    搜索 vcpkg 包
trestle vcpkg --install --ports fmt,zlib
                                安装 vcpkg 包
trestle toolchain               列出检测到的工具链
trestle doctor                  检查项目环境
```

使用 WSL 编译器：

```powershell
trestle configure -toolchain /usr/bin/clang++ -c /usr/bin/clang `
  -mode wsl -wsl-distribution Ubuntu-24.04
```

连接同一 WSL 发行版中的 CUDA 与 Vulkan SDK（根目录可省略，Trestle 会从环境变量和 `PATH` 自动发现）：

```powershell
trestle configure -toolchain /usr/bin/g++ -c /usr/bin/gcc `
  -mode wsl -wsl-distribution Ubuntu-24.04 `
  -cuda-execution wsl -cuda-wsl-distribution Ubuntu-24.04 `
  -vulkan-execution wsl -vulkan-wsl-distribution Ubuntu-24.04
```

Project Console 的 Toolchains 页面也会列出各 WSL 发行版内发现的 CUDA/Vulkan 组件；选中组件后按 Enter 即可连接。CUDA 必须与 WSL 主机编译器位于同一发行版，Vulkan Shader 工具可独立在 WSL 中运行。

保存和复用编译器参数：

```powershell
trestle configure -toolchain clang-cl -compile-flags "/W4 /utf-8" `
  -link-flags "/DEBUG" -save-preset dev
trestle build -preset dev app
```

## `trestle.toml` 示例

```toml
schema_version = 6

[project]
name = "hello"

[build]
profile = "debug"
build_dir = "build"
c_standard = "c17"
cxx_standard = "c++20"
default_targets = ["app"]
compile_commands = "compile_commands.json"

[toolchain]
c = "auto"
cxx = "auto"
mode = "native"
cuda_execution = "native"
vulkan_execution = "native"

[vcpkg]
root = "C:\\src\\vcpkg"
triplet = "x64-windows"

[targets.app]
type = "executable"
sources = ["src/*.cpp"]
include_dirs = ["include"]
compile_options = ["/W4"]

[[targets.app.dependencies]]
package = "fmt"
scope = "private"

[packages.fmt]
provider = "vcpkg"
port = "fmt"
triplet = "x64-windows"

[package]
format = "zip"
output = "dist/hello.zip"
optimization = "balanced"
targets = ["app"]
```

目标依赖可使用 `private`、`public` 或 `interface` 作用域。目标类型包括 `static`、`shared`、`executable`、`test` 和 `shader`。

### 条件规则与任务

条件表达式只支持 `&&`、`||`、`!`、`==`、`!=`、括号和字符串；变量为 `os`、`arch`、`mode`，只读函数为 `tool("name")`、`wsl("发行版")`、`wsl_tool("发行版", "工具")`、`package("包名")`、`env("变量")`、`path("路径")`。规则按 TOML 顺序应用，条件为假时不改变配置。`tool()` 在当前模式下查找工具，WSL 模式会在选定发行版中查找。`path()` 的相对路径以 `trestle.toml` 所在目录为基准。`package()` 检查声明的包能否解析。

```toml
[[rules]]
when = 'os == "windows" && wsl_tool("Ubuntu", "clang++")'
mode = "wsl"
wsl_distribution = "Ubuntu"
cxx = "clang++"

[[rules]]
when = 'mode == "wsl" && tool("clang++")'
target = "app"
compile_flags = ["-Wall"]
cxx_flags = ["-Wextra"]
link_flags = ["-Wl,--as-needed"]
packages = ["fmt"]

[targets.app]
when = 'os == "windows" || os == "linux"'
requires_tools = ["ninja"]

[tasks.prepare]
description = "检查工具并切换 Release 配置"
command = ["clang++", "--version"]
working_dir = "."
timeout = "10s"
set = { "build.profile" = "release", "toolchain.cxx" = "clang++" }
```

`[[rules]]` 可设置 `c`、`cxx`、`mode`、`wsl_distribution`、各类 flags、`targets`（默认目标），以及为 `target` 添加 `packages`（外部包）、`depends_on`（项目目标）依赖。设置 `target` 后，`compile_flags`、`c_flags`、`cxx_flags`、`link_flags` 只影响该目标；不设置 `target` 时，它们影响整个项目。编译器、模式和默认目标仍属于项目级设置。Settings 页面按 `v` 可查看每条规则是否匹配、最终编译器与参数，以及配置相对 TOML 原值的变化。`targets.<name>` 还可设置 `requires_wsl`。Targets / Build 页面会列出不可构建原因；按 `f` 可跳过预检尝试编译，CLI 使用 `trestle build --force`。跳过预检不保证缺失工具或包时能成功。

Tasks 页面按 Enter 预览完整命令、工作目录、超时和配置差异，再按 `y` 执行。`working_dir` 可填项目相对路径或绝对路径，默认项目目录；`timeout` 使用 `30s`、`5m` 等正时长，省略则不限制运行时间。运行中按 Esc 或 Ctrl+C 可取消任务，Trestle 会尝试终止任务进程及其子进程；取消、超时或命令失败时不会保存 `set` 指定的配置。命令自行修改的其他文件无法自动回滚。任务来自不可信项目时可能损害设备；任务命令是参数数组，不会隐式通过 shell 执行。

## 项目结构

```text
cmd/trestle/          CLI 入口
internal/app/         应用服务、构建、测试与发布流程
internal/config/      TOML schema、迁移与校验
internal/deps/vcpkg/  vcpkg 搜索、安装与元数据解析
internal/graph/       目标图和依赖传播
internal/importer/    CMake/Xmake 导入
internal/ninja/       Ninja 文件生成
internal/plan/        编译、链接和运行时部署计划
internal/toolchain/   编译器、VS ABI、WSL 与 CUDA
internal/tui/         终端界面
scripts/release/      Alpha 安装包及 PATH 安装脚本
.github/workflows/    桌面端 Alpha 构建、发布与日志归档
testdata/             测试夹具
```

## 设计原则

Trestle 生成普通 Ninja 构建图，并保留完整命令供检查。项目配置、生成文件和真实工具链之间保持清晰边界：配置可版本控制，构建目录可随时删除重建，编译器及依赖错误会以目标和阶段为单位报告。

## 许可证

本项目采用 [MIT License](LICENSE)。
