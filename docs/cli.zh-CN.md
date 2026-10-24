# 命令行参考

[English](cli.en.md) · [返回 README](../README.md)

Trestle 只有一个可执行文件 `trestle`。它在项目目录中运行，读取 [`trestle.toml`](configuration.zh-CN.md) 并驱动构建、测试、导入、打包等流程；在终端中直接运行会打开 Project Console（TUI）。

## 全局约定

- **`-C <dir>`**：所有子命令都接受，默认 `.`。对 `init` 来说它是创建项目的目标目录；对其他命令，Trestle 会先切换到该目录再执行。
- **无参数运行**：在终端中打开 TUI（自动发现项目配置）；在非终端环境打印用法。
- **诊断与退出码**：成功退出码为 `0`；失败时诊断渲染到标准错误并退出 `1`。失败也会被记录为一条运行日志，可用 `trestle logs` 查看。
- **工具链发现缓存**：结果缓存在 `[toolchain].cache_dir`、`TRESTLE_TOOLCHAIN_CACHE` 或默认 `.trestle/toolchain`。

## 命令一览

| 命令 | 用途 |
| --- | --- |
| `trestle` / `trestle tui` | 打开 Project Console（TUI） |
| `trestle init` | 创建项目 |
| `trestle configure` | 配置编译器、环境、CUDA、WSL 与预设 |
| `trestle analyze` | 分析现有源码树 |
| `trestle build` | 构建目标 |
| `trestle test` | 运行测试 |
| `trestle import` / `import-cmake` | 通过 CMake File API 导入 |
| `trestle import-xmake` | 导入 Xmake 目标 |
| `trestle export-cmake` | 反向导出为 `CMakeLists.txt`（实验） |
| `trestle package` | 生成发布归档 |
| `trestle release` | 构建并生成发布包 |
| `trestle editor-config` | 生成 VS Code 的 `.vscode/tasks.json` |
| `trestle vcpkg` | 搜索或安装 vcpkg 包 |
| `trestle toolchain` | 列出检测到的工具链 |
| `trestle doctor` | 检查项目环境 |
| `trestle logs` | 查看运行日志 |
| `trestle version` | 打印版本信息 |
| `trestle help` / `-h` / `--help` | 打印用法 |

## `trestle` 与 `trestle tui`

```text
trestle [-C dir]
trestle tui [-C dir]
```

打开 Project Console。`trestle` 不带子命令时会先发现项目配置，再打开 TUI。

## `trestle init`

```text
trestle init [-C dir] [-name project]
```

创建一个最小可用的项目（`trestle.toml` 与示例源文件）。`-C` 指定创建目录，`-name` 指定项目名。若 `.vscode/tasks.json` 尚不存在，init 还会写入 VS Code 的构建、测试、发布和运行任务；已存在的文件不会被改动。

## `trestle configure`

```text
trestle configure [-C dir] [选项 ...]
```

更新项目设置。只提供需要的选项，未提供的保持不变。

| 选项 | 说明 |
| --- | --- |
| `-toolchain <名称或路径>` | 设置 C++ 编译器。 |
| `-c <名称或路径>` | 设置 C 编译器。 |
| `-profile <debug\|release>` | 设置构建配置。 |
| `-compile-flags "<参数串>"` | 通用编译参数（如 `"/W4 /utf-8"`）。 |
| `-c-flags "<参数串>"` / `-cxx-flags "<参数串>"` | 仅 C / 仅 C++ 的编译参数。 |
| `-link-flags "<参数串>"` | 链接参数。 |
| `-mode <native\|wsl>` | 编译器执行模式。 |
| `-wsl-distribution <名称>` | WSL 发行版。 |
| `-preset <名称>` | 应用一个编译器预设。 |
| `-save-preset <名称>` | 把结果保存为编译器预设。 |
| `-vcpkg-root <路径>` / `-triplet <名称>` | vcpkg 根目录与 triplet。 |
| `-msvc <版本或区间>` | MSVC 工具集，如 `14.3~14.5`。 |
| `-cuda <版本区间或路径>` / `-cuda-mode <whole\|rdc>` | CUDA 版本与模式。 |
| `-cuda-execution <native\|wsl>` / `-cuda-wsl-distribution <名称>` | CUDA 执行位置。 |
| `-vulkan <路径>` / `-vulkan-execution <native\|wsl>` / `-vulkan-wsl-distribution <名称>` | Vulkan SDK 位置与执行方式。 |
| `-setup <脚本>` | `vcvars64.bat` 或环境设置脚本。 |
| `-module-scanner <可执行文件>` / `-modules` | 模块扫描器与是否启用模块扫描。 |
| `-interactive` | 使用配置向导。 |

```powershell
trestle configure -toolchain clang-cl -compile-flags "/W4 /utf-8" `
  -link-flags "/DEBUG" -save-preset dev
trestle build -preset dev app
```

## `trestle analyze`

```text
trestle analyze [-C dir]
```

分析现有源码树并把结果打印到标准输出。

## `trestle build`

```text
trestle build [-C dir] [-p member] [--all] [--force] [--preset name] [--profile debug|release] [target ... | member:target ...]
```

| 选项 | 说明 |
| --- | --- |
| `[target ...]` | 要构建的目标；省略时使用 `build.default_targets`。 |
| `member:target` | 构建指定 workspace 成员的指定目标（例如 `core:util`）。 |
| `-p`, `--package <成员>` | 只构建该 workspace 成员；省略时按依赖顺序构建全部成员。 |
| `--all` | 构建全部目标。 |
| `--force` | 跳过预检，尝试构建不可用的目标（缺失工具或包仍可能失败）。 |
| `--preset <名称>` | 构建前应用一个编译器预设。workspace 中按成员生效，不写回根配置。 |
| `--profile <debug\|release>` | 覆盖构建配置。workspace 中作用于本次构建的成员。 |
| `--ui` | 在终端中先打开 Project Console。 |

`[workspace]` 根还会在构建完成后生成聚合的 `compile_commands.json`。

## `trestle test`

```text
trestle test [-C dir] [-p member] [-job name | -group name | -file source | -all]
```

| 选项 | 说明 |
| --- | --- |
| `-job <名称>` | 运行单个测试 job。 |
| `-group <名称>` | 运行一个测试组。 |
| `-file <源文件>` | 运行拥有该源文件的测试。 |
| `-all` | 运行全部测试。 |
| `-p`, `--package <成员>` | 只运行该 workspace 成员的测试。 |

不提供任何选择选项时等同于 `-all`。workspace 根按成员依赖顺序逐个运行。

## `trestle import` 与 `trestle import-cmake`

```text
trestle import [-C dir]
trestle import-cmake [-C dir]
```

通过 CMake File API 导入。`trestle import` 在当前目录存在 `xmake.lua` 时自动改为导入 Xmake。

## `trestle import-xmake`

```text
trestle import-xmake [-C dir]
```

导入 Xmake 目标（兼容新版与旧版 Xmake）。

## `trestle export-cmake`

```text
trestle export-cmake [-C dir] [-output CMakeLists.txt] [-force]
```

把 `trestle.toml` 反向导出为等价的 `CMakeLists.txt`（实验性）。`-output` 指定输出路径（项目相对，默认 `CMakeLists.txt`），`-force` 覆盖已存在的文件。仅供参考且可能丢失信息：Trestle 专有的段落会以注释保留。

## `trestle package`

```text
trestle package [-C dir] [-output dist/app.zip]
```

根据 `[package]` 设置构建发布归档。`-output` 覆盖输出路径。`package` 引用同时接受 vcpkg 端口（`zlib`）与 Conan 引用（`fmt/10.2.1@x/stable`）。

## `trestle release`

```text
trestle release [-C dir] [-optimization balanced|speed|size|custom:name] [-targets app,tool] [-output dist/app.zip]
```

配置并运行一次发布构建，然后打包。`-optimization` 选择优化预设，`-targets` 指定逗号分隔的目标，`-output` 覆盖归档路径。

## `trestle editor-config`

```text
trestle editor-config [-C dir] [-merge | -force]
```

生成 `.vscode/tasks.json`，包含 `Trestle: Build`、`Trestle: Build All`、`Trestle: Build Current Target`、`Trestle: Test`、`Trestle: Release`，以及每个可执行目标对应的 `Trestle: Run <目标>` 任务。运行任务依赖 `Trestle: Build`，执行 `${workspaceFolder}` 下当前 profile 的产物，并带有追加 `.exe` 后缀的 `windows` 覆盖；因此同一份文件可在各操作系统使用，适合提交到仓库。

已存在的 `.vscode/tasks.json` 绝不会被静默覆盖：不带参数时命令直接报错，`-merge` 只替换 trestle 生成的任务和输入（你自定义的任务、输入及顶层设置都会保留），`-force` 才会整体替换。标签以 `Trestle: ` 开头的任务视为 trestle 生成的任务。`-merge` 要求文件是纯 JSON；含注释的文件会被拒绝，以免丢失内容。`trestle init` 会在文件缺失时自动生成；检测到 `launch.json` 时还会提示设置 `"preLaunchTask": "Trestle: Build"`，以便调试前先构建。

在 workspace 根上运行时，生成的任务会为每个成员添加 `Trestle: Build <成员>` 与 `Trestle: Run <成员>:<目标>`，目标选择器使用 `成员:目标` 形式，运行任务依赖对应成员的构建任务。

## `trestle vcpkg`

```text
trestle vcpkg [-C dir] [-root path] [-search query] | [-install -ports zlib,fmt] [-triplet name]
```

| 选项 | 说明 |
| --- | --- |
| `-root <路径>` | vcpkg 根目录。 |
| `-search <查询>` | 搜索端口。 |
| `-install` | 安装端口。 |
| `-ports <逗号分隔>` | 要安装的端口，如 `fmt,zlib`。 |
| `-triplet <名称>` | vcpkg triplet。 |

## `trestle toolchain`

```text
trestle toolchain [-C dir] [--cache-dir path] [--refresh]
```

列出检测到的工具链。`--refresh` 重新扫描已安装工具链并刷新缓存，`--cache-dir` 覆盖缓存目录。安装新工具链后使用它。

## `trestle doctor`

```text
trestle doctor [-C dir] [-p member]
```

检查项目环境（工具、编译器 ABI、依赖、Vulkan/CUDA、WSL 等）并报告问题。workspace 根会按依赖顺序逐个成员检查并汇总失败；`-p` 只检查一个成员。

## `trestle logs`

```text
trestle logs [-C dir] [operation-id]
```

打印运行日志。省略 `operation-id` 时列出/显示最近的日志。

## `trestle version`

```text
trestle version
trestle --version
```

打印 `trestle <版本> (<提交>, <构建时间>)`。

## 另见

- [`trestle.toml` 配置参考](configuration.zh-CN.md)
- [从零构建带 fmt 依赖的 C++ 项目](tutorial.zh-CN.md)
- [Workspace 指南](workspace.zh-CN.md)
