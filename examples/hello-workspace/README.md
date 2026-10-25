# hello-workspace

一个最小的 Trestle workspace 示例：`core` 提供静态库，`cli` 通过跨成员依赖链接它，另有一个测试目标。

```text
hello-workspace/
  trestle.toml        # workspace 根：[workspace] 成员 + 共享 toolchain/build_dir
  core/
    trestle.toml      # 静态库 greeter + 测试 greeter_test
    include/greeter/greeter.h
    src/greeter.cpp
    tests/greeter_test.cpp
  cli/
    trestle.toml      # 可执行文件 hello，依赖 core:greeter
    src/main.cpp
```

## 构建与运行

```powershell
# 在仓库根目录执行
trestle build -C examples/hello-workspace          # 按依赖顺序构建全部成员
trestle build -C examples/hello-workspace -p core  # 只构建 core
trestle build -C examples/hello-workspace cli:hello # 只构建 cli 的 hello 目标
```

产物位置：

```text
build/debug/core/bin/greeter.a        # Windows/MSVC 为 .lib
build/debug/cli/bin/hello-workspace.exe
```

运行：

```powershell
# Windows
& examples/hello-workspace/build/debug/cli/bin/hello-workspace.exe
# Linux/macOS
./examples/hello-workspace/build/debug/cli/bin/hello-workspace
```

输出：

```text
Hello, workspace!
```

## 测试与环境检查

```powershell
trestle test -C examples/hello-workspace            # 跳过没有测试的成员
trestle test -C examples/hello-workspace -p core    # 只跑 core 的测试
trestle doctor -C examples/hello-workspace          # 逐成员检查
trestle release -C examples/hello-workspace         # 逐成员 release 并生成各自 ZIP
```

## 用 TUI 操作

```powershell
trestle -C examples/hello-workspace
```

侧边栏在 workspace 模式下显示可折叠的成员树，默认只展开 `Root`：

```text
▼ Root
  1 Overview
  2 Targets (all)
  3 Build
  4 Packages
  5 Tasks
  6 Toolchains
  7 Presets
  8 Release
  9 Doctor
  0 Settings
▶ core
▶ cli
```

选中 `Root` 或 `core` / `cli` 标题行按 Enter（或 `z`）展开/折叠；`Root` 默认展开，成员默认折叠。展开后编号在每个分组内从 1 开始，数字键作用于当前选择所在分组的分页（例如焦点在 core 标题时按 `1` 打开 core 的 Targets）。↑↓ 落到页面行会立即切换页面，落到标题行只移动选择；列表过长会自动滚动保持选择可见；`w` 输入成员名快速切换并展开。

Root Targets 聚合所有成员目标（`core:greeter`、`cli:hello`），按 `/` 可按成员名或目标名过滤；选中后 Enter 只构建该成员的该目标。成员页的修改写入该成员 `trestle.toml`；Root Packages 的包会被所有成员继承，成员页标为 `shared from root`。日志行带 `[core]` / `[cli]` 前缀。

## 需要自定义编译器时

示例使用 `cxx = "auto"`。若自动探测结果不合适，在根目录配置一次即可，成员会继承：

```powershell
trestle configure -C examples/hello-workspace -toolchain g++ -c gcc
```

更多说明见 [Workspace 指南](../../docs/workspace.zh-CN.md)。
