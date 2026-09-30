# 从零构建带 fmt 依赖的 C++ 项目

本教程完成安装工具、初始化、配置编译器、声明与安装依赖、构建、运行和发布。完整代码在 [examples/hello-fmt](../examples/hello-fmt/)。命令需要联网下载 vcpkg 和 fmt；首次安装可能耗时数分钟。

## 1. 安装 Trestle 和构建工具

从 [Trestle Releases](https://github.com/littlefish1145/trestle/releases) 下载与宿主系统、CPU 匹配的安装包，执行其中的安装脚本，再打开新终端。也可以安装 Go 1.26+ 后从源码构建：

```sh
git clone https://github.com/littlefish1145/trestle.git
cd trestle
go build -o trestle ./cmd/trestle
```

Windows 使用 `go build -o trestle.exe ./cmd/trestle`。把可执行文件所在目录加入 PATH。确认 `trestle version` 能运行。

### Windows：PowerShell，MSVC ABI

安装 Visual Studio Build Tools，选择“使用 C++ 的桌面开发”和 Windows SDK。使用 clang-cl 时另需安装 LLVM。安装 Git、CMake 和 Ninja，并确认命令可用：

```powershell
git --version
cmake --version
ninja --version
git clone https://github.com/microsoft/vcpkg.git C:\src\vcpkg
& C:\src\vcpkg\bootstrap-vcpkg.bat
$env:VCPKG_ROOT = 'C:\src\vcpkg'
```

以下示例采用 x64 编译器和 `x64-windows`。ARM64 原生目标使用 `arm64-windows`，并选择 ARM64 编译器环境。MinGW 使用 `x64-mingw-dynamic` 或 `x64-mingw-static`，不能混用 MSVC 的库。显式 triplet 优先于自动选择；自动选择有歧义时会要求指定。

### Linux：Ubuntu/Debian，Bash

```sh
sudo apt-get update
sudo apt-get install -y build-essential git cmake ninja-build curl zip unzip tar pkg-config
git clone https://github.com/microsoft/vcpkg.git "$HOME/vcpkg"
"$HOME/vcpkg/bootstrap-vcpkg.sh" -disableMetrics
export VCPKG_ROOT="$HOME/vcpkg"
```

x64 使用 `x64-linux`，ARM64 使用 `arm64-linux`。其他发行版使用各自包管理器安装同类工具。

### macOS：Bash/zsh

```sh
xcode-select --install
brew install git cmake ninja pkg-config
git clone https://github.com/microsoft/vcpkg.git "$HOME/vcpkg"
"$HOME/vcpkg/bootstrap-vcpkg.sh" -disableMetrics
export VCPKG_ROOT="$HOME/vcpkg"
```

Apple Silicon 使用 `arm64-osx`；Intel 使用 `x64-osx`。Rosetta 终端和原生终端架构可能不同，确保编译器与依赖架构一致。[vcpkg 宿主要求](https://learn.microsoft.com/en-us/vcpkg/concepts/supported-hosts) 和 [triplet 文档](https://learn.microsoft.com/en-us/vcpkg/concepts/triplets) 提供更多说明。

## 2. 初始化项目

在准备存放项目的目录执行：

```sh
trestle init -C hello-fmt -name hello-fmt
cd hello-fmt
```

生成 `trestle.toml`、`src/main.cpp` 和 `.gitignore`。已有配置会拒绝覆盖。将 `src/main.cpp` 替换为：

```cpp
#include <fmt/core.h>
int main() {
    fmt::print("Hello from Trestle + fmt!\n");
    return 0;
}
```

## 3. 配置编译器与依赖目标

Windows x64（PowerShell）：

```powershell
trestle configure -toolchain clang-cl -profile debug -vcpkg-root $env:VCPKG_ROOT -triplet x64-windows
```

也可以使用 `-toolchain cl`。LLVM 必须与 Visual Studio STL 版本兼容；如遇 STL1000 提示 Clang 版本过旧，升级 LLVM 或选择 MSVC。Trestle 自动寻找 Visual Studio 环境；失败时添加 `-setup 'C:\实际VS路径\VC\Auxiliary\Build\vcvars64.bat'`。确保该脚本属于实际安装，且架构匹配。

Linux x64：

```sh
trestle configure -toolchain g++ -profile debug -vcpkg-root "$VCPKG_ROOT" -triplet x64-linux
```

macOS Apple Silicon（Intel 改为 `x64-osx`）：

```sh
trestle configure -toolchain clang++ -profile debug -vcpkg-root "$VCPKG_ROOT" -triplet arm64-osx
```

Windows 的动态 CRT triplet 还需在现有 `[vcpkg]` 表里设置 `crt_linkage = "dynamic"`，确保 debug 使用 `/MDd`、release 使用 `/MD`。

## 4. 声明并安装 fmt

在 `trestle.toml` 末尾添加以下表。若这些表已存在，编辑现有表，不要重复声明：

```toml
[[targets.app.dependencies]]
package = "fmt"
scope = "private"

[packages.fmt]
provider = "vcpkg"
port = "fmt"
```

包的 triplet 继承 `[vcpkg].triplet`。Windows：

```powershell
trestle vcpkg -root $env:VCPKG_ROOT -install -ports fmt -triplet x64-windows
```

Linux/macOS：

```sh
# macOS 改为 arm64-osx 或 x64-osx；ARM64 Linux 改为 arm64-linux
trestle vcpkg -root "$VCPKG_ROOT" -install -ports fmt -triplet x64-linux
```

安装成功的包位于 `$VCPKG_ROOT/installed/<triplet>/`。CLI 的 `vcpkg -install` 负责安装；以上 TOML 显式声明依赖。TUI 的 Packages 页选择包并安装时，会同时添加项目依赖。

## 5. 构建和运行

```sh
trestle doctor
trestle build app
```

Windows：`& .\build\debug\bin\hello-fmt.exe`。

Linux/macOS：`./build/debug/bin/hello-fmt`。

预期输出：

```text
Hello from Trestle + fmt!
```

生成的 Ninja 文件为 `build/debug/build.ninja`，编译数据库为项目根目录 `compile_commands.json`。需要的 Windows DLL 会复制到可执行文件旁边。修改源码后再次运行 `trestle build app`，Ninja 会增量构建。

发布：

```sh
trestle release
```

默认生成 `build/release/bin/hello-fmt[.exe]` 和 `dist/hello-fmt.zip`。发布包内包含 `bin/` 下的程序及运行时依赖。发布会持久化 release profile 和优化选项；返回 debug 用 `trestle configure -profile debug`。

## 6. WSL 完整依赖流程

打开目标发行版，在发行版内部安装 **Linux 版 Trestle**，执行本教程 Linux 的全部步骤。建议项目位于 `~/hello-fmt`，vcpkg 位于 `~/vcpkg`，使用 `x64-linux` 或 `arm64-linux`。Windows 的 `C:\src\vcpkg\installed\x64-windows` 不能提供 Linux 链接库。

Windows 驱动 WSL 的编译检查可在另一个无依赖项目中执行（发行版名以 `wsl --list --verbose` 为准）：

```powershell
wsl --list --verbose
wsl -d Ubuntu-24.04 --exec sh -lc 'command -v g++; command -v gcc; command -v ar'
trestle configure -mode wsl -wsl-distribution Ubuntu-24.04 -toolchain /usr/bin/g++
trestle doctor
trestle build app
```

WSL 模式的 C/C++ 编译器、归档器和链接器必须是该发行版内的可执行文件；Windows 源码和输出路径由 Trestle 转换。`\\wsl.localhost\另一发行版\...` 路径不能用于选定发行版。Windows 驱动 WSL 不负责安装 Linux 包，完整 fmt 教程采用发行版内构建。

真实 WSL 验证入口（Trestle 仓库内 PowerShell）：

```powershell
$env:TRESTLE_TEST_WSL = '1'
$env:TRESTLE_WSL_DISTRIBUTION = 'Ubuntu-24.04'
go test -v ./internal/app -run '^TestRealWSLBuild$' -count=1
```

未启用、未安装 WSL 或没有指定发行版时，测试明确报告 `SKIP`；跳过不代表 WSL 验证通过。

## 7. 日志、TUI 与恢复

`trestle tui` 打开工作台：先 Toolchains，随后 Doctor → Packages → Build。Settings 的分类菜单按 Enter 进入，`/` 实时搜索键、名称和值，Esc 返回。Tab/Shift+Tab 切换焦点；F2 全屏日志，F3 完整错误或字段详情，F4 历史，F5 收起日志；日志上滚暂停跟随，End 恢复，`/` 搜索、`n` 下一个匹配，`?` 查看可滚动帮助。

每次操作日志保存在 `.trestle/runs/<ID>/output.log`，`record.json` 保存时间、配置指纹及终态。`trestle logs` 列出记录，`trestle logs <ID>` 输出完整日志。日志不自动删除。关闭相关操作后，可手动删除旧 ID 目录；不要在运行中删除。

| 问题 | 恢复路径 |
| --- | --- |
| 工具链/Ninja 未找到 | 检查 PATH，运行 `trestle toolchain`，指定实际编译器；MSVC 检查 VS C++ workload 和 setup 脚本 |
| 包解析/链接失败 | 核对根目录、目标 OS/架构/ABI、triplet、CRT 和 debug/release，安装对应 fmt 后重试 |
| 资源占用 | 错误提供资源、操作、PID 和开始时间；在拥有操作的终端取消或等待完成，再重试，不需删锁文件 |
| 配置提交冲突 | 重新读取文件或刷新 TUI，检查其他操作的修改，再重新编辑/预览；旧版本不会覆盖最新文件 |
| 配置迁移失败 | 原文件保留；首次迁移保存前有 `trestle.toml.schema-v*.bak` 原始备份，核对版本和 TOML 后恢复 |
| 构建失败/中断 | 修复完整日志中的首个错误，重新 `trestle build app`；未完成生成自动重做，已编译产物可复用 |
| 任务中断 | 刷新并重新预览任务，核对命令产生的外部文件后运行；外部副作用不自动回滚 |
| 发布失败 | 旧 ZIP 保留；修复错误后重新 `trestle release` |
| 日志保存失败 | 操作拒绝启动或取消；按错误路径检查磁盘空间和权限，再重试 |

CLI Ctrl+C、TUI Esc 取消受管理的进程树并等待日志收尾。强制退出后的运行记录在下次查看历史时标记为中断；不要仅根据锁文件是否存在判断占用。
