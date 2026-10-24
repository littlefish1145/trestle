# Workspace：一个根配置管理多个项目

Trestle 的 workspace 让一个根 `trestle.toml` 引用多个成员项目，共享工具链与依赖声明，并按依赖顺序统一构建。没有 `[workspace]` 段时项目仍是单项目，行为完全不变。

本指南配合可运行示例 [examples/hello-workspace](../examples/hello-workspace/)：`core` 提供静态库，`cli` 链接它并生成可执行文件。

## 1. 目录布局

```text
hello-workspace/
  trestle.toml        # 根：[workspace] 成员列表与共享配置
  core/
    trestle.toml      # 成员：静态库 + 测试
    include/greeter/greeter.h
    src/greeter.cpp
    tests/greeter_test.cpp
  cli/
    trestle.toml      # 成员：可执行文件，依赖 core:greeter
    src/main.cpp
```

构建产物按成员隔离：

```text
build/
  debug/
    core/bin/...            # core 的库与中间产物
    cli/bin/hello-workspace # cli 的可执行文件
    compile_commands.json   # 聚合了全部成员的编译命令
```

## 2. 根配置

```toml
schema_version = 7

[project]
name = "hello-workspace"

[build]
profile = "debug"
build_dir = "build"

[toolchain]
c = "auto"
cxx = "auto"
mode = "native"

[workspace]
members = ["core", "cli"]
```

- `members` 是相对 workspace 根的目录，**每一项都必须有自己的 `trestle.toml`**；绝对路径和 `..` 逃逸会被拒绝。
- 目标名在整个 workspace 内唯一，重名会报 `E_WORKSPACE_INVALID`。
- 根可以只做容器（如上），也可以声明自己的目标；根目标最后构建，产物在 `build/<profile>/`。

## 3. 成员配置

### 静态库成员（core）

```toml
schema_version = 7

[project]
name = "hello-core"

[build]
default_targets = ["greeter"]

[targets.greeter]
type = "static"
sources = ["src/greeter.cpp"]
include_dirs = ["include"]

[targets.greeter_test]
type = "test"
sources = ["tests/greeter_test.cpp"]
test_group = "unit"
dependencies = [{ target = "greeter", scope = "public" }]
```

成员内的相对路径（源文件、include_dirs、library_dirs 等）以**成员目录**为基准，与单项目一致。

### 依赖其他成员（cli）

```toml
schema_version = 7

[project]
name = "hello-cli"

[build]
default_targets = ["hello"]

[targets.hello]
type = "executable"
sources = ["src/main.cpp"]
output_name = "hello-workspace"
dependencies = [{ target = "core:greeter", scope = "public" }]
```

跨成员依赖写成 `成员:目标`。`public` 作用域会把 `greeter` 的 include 目录、defines 与链接产物传递给 `hello`；`private` 只供本目标使用，`interface` 只传给消费者。传递依赖（A→B→C）同样成立。

## 4. 配置合并规则

| 配置 | 来源 |
| --- | --- |
| `[toolchain]`、`[vcpkg]`、`[conan]` | 成员缺省时继承根，成员声明优先 |
| `[compiler_presets.*]` | 根与成员取并集，同名时成员优先 |
| `[build]` 的 profile、标准、flags | 以成员为准 |
| `build_dir`、`compile_commands` | 以根为准，成员写入 `build/<profile>/<成员>/` |
| `[packages.*]` | 根的包对成员可见，成员同名声明优先 |
| `[workspace.dependencies]` | 共享依赖，按 `port` 命名后加入每个成员 |

共享依赖示例：

```toml
[[workspace.dependencies]]
provider = "vcpkg"
port = "fmt"
```

成员里直接 `package = "fmt"` 引用即可，不必重复声明（仍需按单项目流程安装 vcpkg 端口）。

## 5. 常用命令

```powershell
# 构建
trestle build -C examples/hello-workspace            # 全部成员，按依赖顺序
trestle build -C examples/hello-workspace -p core    # 单个成员
trestle build -C examples/hello-workspace cli:hello  # 成员:目标 简写

# 测试与检查
trestle test -C examples/hello-workspace             # 跳过没有测试的成员
trestle test -C examples/hello-workspace -p core     # 只跑 core
trestle doctor -C examples/hello-workspace           # 逐成员检查环境
trestle doctor -C examples/hello-workspace -p cli    # 只检查 cli

# 编辑器配置
trestle editor-config -C examples/hello-workspace
```

`trestle build` 完成后会把所有成员的编译命令合并写入：

- `build/<profile>/compile_commands.json`
- 根配置的 `build.compile_commands`（默认根目录 `compile_commands.json`）

`editor-config` 会为每个成员生成 `Trestle: Build <成员>`，为可执行目标生成 `Trestle: Run <成员>:<目标>`，目标选择器使用 `成员:目标` 形式。

## 6. TUI

```powershell
trestle -C examples/hello-workspace
```

workspace 模式下侧边栏是一棵成员树，数字键直接打开对应行：

```text
▼ Root
  1 Overview
  2 Toolchains
  3 Build All
▼ core
  4 Targets (2)
  5 Packages
▼ cli
  6 Targets (1)
  7 Packages
```

`Build All` 行打开的是面向全部成员的 Build 页面：页面会列出各成员及其目标数，按 `b` 或 Enter（焦点在内容区时）即构建全部成员。

- `Root` 分组是 workspace 级页面：Overview、Toolchains，以及面向全部成员的 Build 页面。
- 每个成员分组下只显示该成员的 Targets 与 Packages；Targets 页显示该成员的目标，测试页也只列该成员的测试。
- 与非 workspace 模式一致：↑↓ 立即切换页面（循环），数字键直接跳转，Enter 把焦点移到内容区；`b` 构建当前选择（Build 页且未选成员时即构建全部成员）。
- 标题栏显示 `ws: <名称>` 与 `pkg: <成员|all>`。
- 按 `w` 打开成员输入框可以快速切换（留空=全部成员）；之后 `b` 构建、Tests 页面、Doctor 都作用于所选成员。
- 成员 Packages 页面为只读：包声明写在该成员自己的 `trestle.toml` 中；清空成员选择后可在 Root 上照常管理共享包。
- 构建日志以 `[成员]` 为前缀，例如 `[core] Building CXX object ...`。

## 7. 输出布局与依赖顺序

- 成员产物：`build/<profile>/<成员>/`，根目标：`build/<profile>/`。
- 成员按依赖边拓扑排序构建；`cli` 依赖 `core:greeter`，因此 `core` 先构建。
- 跨成员链接由生成阶段注入提供方产物的真实路径（`.a`、`.lib`、`.so`、`.dylib`、`.dll` 的导入库等），无需手写库名。
- 成员之间形成环时构建直接报 `E_WORKSPACE_CYCLE`。

## 8. 故障排查

| 现象 | 处理 |
| --- | --- |
| `E_WORKSPACE_INVALID: ... not found` | 成员目录缺少 `trestle.toml`，或 `members` 写错。 |
| `E_WORKSPACE_INVALID: target "x" is declared by both ...` | 两个成员用了同一个目标名，重命名其一。 |
| `E_WORKSPACE_INVALID: dependency target "a:b" ... unknown target` | `成员:目标` 写错，或目标不属于该成员。 |
| `E_WORKSPACE_CYCLE` | 成员互相依赖，拆开依赖环。 |
| 成员编译找不到头文件 | 检查 `include_dirs` 是否写在声明头文件的成员里，并用 `成员:目标` 引用。 |
| 链接找不到库 | 确认依赖作用域为 `public`/`private`，且提供方是 `static`/`shared` 目标。 |

## 另见

- [命令行参考](cli.zh-CN.md)
- [`trestle.toml` 配置参考](configuration.zh-CN.md)
- [可运行示例](../examples/hello-workspace/)
