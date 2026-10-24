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
```

## 用 TUI 操作

```powershell
trestle -C examples/hello-workspace
```

侧边栏在 workspace 模式下显示成员树：

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

数字键直接打开对应行，↑↓ 立即切换页面（循环），Enter 进入内容区——与单项目模式一致。`3 Build All` 打开全员的 Build 页，按 `b` 构建全部成员；选中成员的 Targets 后按 `b` 只构建该成员。按 `w` 可快速切换成员（留空=全部）。日志行带 `[core]` / `[cli]` 前缀。

## 需要自定义编译器时

示例使用 `cxx = "auto"`。若自动探测结果不合适，在根目录配置一次即可，成员会继承：

```powershell
trestle configure -C examples/hello-workspace -toolchain g++ -c gcc
```

更多说明见 [Workspace 指南](../../docs/workspace.zh-CN.md)。
