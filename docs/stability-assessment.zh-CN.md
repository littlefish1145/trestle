# 稳定性评估与回归验证

评估日期：2026-10-31。评估范围包括全仓库 Go 测试、静态检查、并发检查、构建计划、初始化模板和真实 C++/Workspace/CUDA 构建。本文区分自动化测试通过、真实工具链通过和未执行的验证，不把跳过的测试计为通过。

## 已修复的问题

| 范围 | 原问题 | 修复后的行为 |
| --- | --- | --- |
| 初始化 | 示例代码混在向导实现中；成员写入的默认工具链会遮蔽根配置 | 示例移到编译时嵌入的 templates；初始成员省略共享配置表，继承根编译器、CUDA 模式和架构 |
| 初始化文件 | 文件类型冲突没有及时报错，普通写入存在检查后覆盖的窗口 | 对源文件使用 Lstat 与排他创建；拒绝目录、符号链接和越界成员；成员成功后再提交根配置 |
| Windows CUDA | 自动选择 clang-cl，NVCC 报 Host compiler targets unsupported OS | 自动选择已发现的 MSVC cl；保留用户显式指定的编译器 |
| CUDA 探测 | 丢失宿主环境、忽略 nvcc 版本命令失败、使用 Background 丢失取消 | 传递宿主环境、传播命令错误、传递调用者 context，使用进程树取消 |
| CUDA 增量 | 未生成头文件 depfile，MSVC 宿主错误使用 showIncludes 规则 | NVCC 在所有宿主下生成并向 Ninja 提交 depfile |
| CUDA 参数 | 宿主专用参数直接发给 NVCC；RDC 同时发出 -dc 与 -c | 使用 -Xcompiler 传递宿主参数；两种编译模式互斥；非法模式和架构在配置/命令阶段报错 |
| CUDA 链接 | 设备链接缺架构；C++ 消费 CUDA 静态库时遗漏设备链接；宿主链接缺运行库 | 设备链接携带架构和静态提供者；宿主链接加入静态 CUDA 运行库；外部 SDK 库目录保留绝对路径，避免深层 Windows 目录失败 |
| Ninja | 每个输入被替换成整个 $in 列表；同源文件的不同目标复用同一条编译规则 | 多输入保留独立参数；限制路径替换范围；编译规则包含目标 ID |
| Workspace | 指定消费者成员后不构建提供者；profile 覆盖晚于图解析 | 按拓扑顺序构建传递依赖；解析图前统一显式 profile |
| Workspace | 根测试被跳过；未知成员上下文可能 panic | 支持根测试；未知成员返回错误；测试前构建提供者 |
| Workspace 元数据 | 重复发布可能重复汇入上次的根聚合数据库 | 去除旧聚合中的成员记录、去重、稳定排序；发布锁响应取消 |
| Workspace 路径 | 别名、Windows 大小写别名和目录符号链接可能绕过成员约束 | 使用规范路径检查重复与越界；图解析复制配置映射，不改变调用方配置 |
| 示例/测试环境 | 仓库示例固定本机 MSVC 版本；安装包管理器后默认测试访问在线服务 | 示例使用 auto；在线包管理器测试需要 TRESTLE_TEST_LIVE=1 |

## 已执行的验证

本机环境：Windows amd64、Go 1.26.0、Ninja 1.13.2、CUDA 13.2.78、MSVC 14.44.35207；普通原生集成使用 clang-cl，CUDA 自动使用 MSVC cl。

| 检查 | 结果与范围 |
| --- | --- |
| 全仓库 `go test -count=1 -timeout=8m ./...` | 通过；另收集了全仓库覆盖率 |
| 全仓库 `go test -race -count=1 -timeout=10m ./...` | 通过；后续初始化、配置、CUDA 回归另跑定向 race 检查并通过 |
| `go vet ./...`、`go mod verify` | 通过 |
| 20 次随机顺序重复测试 | config、graph、plan、ninja、cuda 的 workspace/CUDA 严格用例通过 |
| Workspace 路径 fuzz | 约 10 秒，2,616 次输入执行，通过 |
| Workspace 目标选择 fuzz | 约 10 秒，29,344 次输入执行，通过 |
| 原生 Workspace | 干净目录直接构建 cli:hello，先构建 core；release/debug 分别构建并执行；核对聚合编译数据库；运行 core 测试；修改公共头文件后重建消费者 |
| 真实 CUDA | standalone/workspace × whole/RDC 四组；编译并归档初始化示例；编译、链接、执行 CUDA 宿主程序；RDC 跨翻译单元设备符号；检查不变输入不重建，修改头文件后结果从 42 变为 43 |
| 交叉构建 | Linux amd64 与 Darwin amd64 的 `go build ./...` 通过；不等同于在这些系统上运行测试 |

测试夹具只复制源码与配置，排除示例目录中已存在的忽略构建产物；Workspace 增量测试直接核对消费者对象文件的更新时间。

测试细节：命令测试逐项比较参数数组，检查顺序、空格和输入不变性；探测测试使用真实子进程验证环境、非零退出和取消；图测试检查重复解析结果与输入配置不变性。集成测试显式启用后，缺少工具链会失败。

本机沙箱初次运行出现临时文件替换、本机 HTTP 回环连接与进程取消权限错误；在完整本地权限下重跑全套验证通过。保留了失败日志，未通过放宽断言把环境错误计作成功。

## 覆盖率与剩余风险

补充 Vulkan 回归后，默认测试语句覆盖率合计 **65.1%**。主要模块：config 80.6%、graph 79.0%、plan 72.1%、ninja 78.5%、CUDA 72.6%、Vulkan SDK 73.1%、vscode 97.0%。此统计不包含显式开启的真实 CUDA/native 集成运行。

覆盖率较低的实际代码：processx 14.9%、diag 20.6%、Clang modules 后端 35.0%、state 40.0%、app 44.7%。GCC/MSVC modules 后端没有独立测试。后续应优先补进程树终止失败、模块扫描器兼容性、诊断错误链和 Vulkan 真实编译矩阵。

本次 CUDA 执行验证运行宿主函数并完成真实设备代码编译/链接，**没有在 GPU 上启动内核**；它不能证明设备驱动、GPU 硬件或 kernel 数值计算正确。CUDA Linux/WSL、其他 CUDA/MSVC 版本及其他 GPU 架构未做真实矩阵验证。Linux/macOS 原生执行由新增 CI 门禁负责，本次本机未执行这些平台的 CI。

初始化涉及多个文件，尚不是跨文件事务：后续步骤失败可能保留之前新建的文件。现有文件受到保留/排他创建保护，根配置在成员创建成功后提交。极端掉电与系统调用故障注入尚未验证。

因此当前结论是：已修复并回归验证本次发现的初始化、Workspace 和 CUDA 构建缺陷；尚不能据此宣称全仓库在所有平台和工具链组合下达到同等稳定性。

## 重现与持续门禁

默认检查：

```powershell
go mod verify
go test -count=1 -timeout=8m '-coverprofile=coverage.out' ./...
go vet ./...
go test -race -count=1 -timeout=10m ./...
```

真实构建检查：

```powershell
$env:TRESTLE_TEST_NATIVE = '1'
$env:TRESTLE_TEST_CUDA = '1'
go test -count=1 -v -timeout=12m ./internal/app -run 'TestRealNativeCPPBuild|TestRealWorkspaceCleanSelectedMemberAndProfiles|TestRealCUDAStarterAndIncrementalBuild'
```

可用 `TRESTLE_TEST_COMPILER` 指定普通原生编译器，`TRESTLE_TEST_CUDA_COMPILER` 指定 NVCC 宿主编译器，`TRESTLE_TEST_CUDA_ARCH` 指定已安装 toolkit 支持的架构。在线包管理器验证单独设置 `TRESTLE_TEST_LIVE=1`；需要本机安装和远端服务可用。

Alpha Release 在三个桌面系统执行默认测试、20 次顺序打乱回归和原生构建；Linux 另执行 fuzz 和 race。CUDA Validation 是手动工作流，需要带 `self-hosted, Windows, X64, cuda` 标签且安装好 Go/MSVC/NVCC/Ninja 的 runner；普通 hosted CI 没有 CUDA toolkit，不会把跳过当成真实 CUDA 通过。

本机原始日志与覆盖率保存在未纳入版本控制的 `.trestle/`：`tests-final.log`、`coverage-final.out`、`race.log`、`race-final.log`、`stress-final.log`、`native-final.log`、`workspace-clean.log`、`fuzz-path.log`、`fuzz-target.log`、`vet-final.log`。当前 checkout 原有的根 `trestle.toml` 修改被保留。

NVCC 参数和设备链接行为参考 [NVIDIA CUDA Compiler Driver 文档](https://docs.nvidia.com/cuda/cuda-compiler-driver-nvcc/)。

## WSL Vulkan 补充验证

本轮使用已安装 Vulkan 工具的 **WSL2 ArchLinux**：glslc 2026.1、glslang 16.2.0、spirv-val。该发行版没有 Ninja，测试期间将已安装的 Ubuntu Ninja 1.11.1 复制到工作区 `.trestle/wsl-tools/` 并加入测试 PATH，没有修改 WSL 系统安装。

修复范围：

- glslang 命令始终加上 `-V`，并识别 Windows `.exe` 工具名称；显式工具名称解析到实际 SDK 工具，缺少显式工具时直接报错。
- shader 计划传递 include 目录、宏与 depfile；验证阶段和单一源模式，拒绝多源 glob 只编译第一个文件的静默遗漏。
- 显式 SDK 根目录不再退回无关的 PATH 工具，目录不能被当成可执行文件。
- WSL SDK 探测传播取消与版本探测失败，保留工具查找错误；重复设置工作目录替换旧 `--cd`，不修改原 SDK 的参数。
- 混合宿主/WSL shader 构建也转换路径；转换和缓存使用每个 action 的 WSL 发行版；独立 `-MF` 等路径标志不被错误拼接。
- shader 的存在不再绕过混合 C++ 项目的编译器检查。

真实 glslc 2026.1 会输出未转义空格的 depfile，Ninja 因而把一个文件名拆成多个依赖并反复重编译。对调用参数中已知的空格依赖路径，自动选择工具时回退到 glslang；显式指定 glslc 时返回 `E_VULKAN_DEPFILE_PATH`，提示改用 glslang。glslc 编译矩阵使用不含空格的相对源码/include 名称，项目根目录仍含空格；glslang 矩阵同时覆盖源码/include 目录含空格。仅出现在 GLSL 内部 include 文件名中的特殊字符，仍属于 glslc depfile 的已知限制，建议此类项目显式使用 glslang。[shaderc 依赖参数说明](https://github.com/google/shaderc/blob/main/glslc/README.asciidoc)

实际执行结果：

| 验证 | 结果 |
| --- | --- |
| 在 WSL 内直接运行 Linux 版集成测试 | **通过**；glslc/glslang × standalone/workspace 四组 |
| 每组 vertex/fragment/compute 编译 | **通过**；检查 SPIR-V magic、长度对齐，并以 `spirv-val --target-env vulkan1.3` 校验 |
| 不变输入不重编译、include 修改改变 SPIR-V | **通过** |
| 错误 GLSL 必须失败、恢复后产物确定、无效 SDK 不修改配置 | **通过** |
| Windows Go 进程驱动 `wsl.exe` 的完整集成 | **未通过环境验证**；`wsl.exe` 返回 `Wsl/E_ACCESSDENIED`；不依赖本项目的最小 Go 程序同样复现，而 PowerShell/Python 直接调用成功。保留 opt-in 测试供正常 Windows 终端或 self-hosted CI 运行，不将它计为通过 |
| ArchLinux Vulkan 设备枚举 | **失败**；`vulkaninfo --summary` 返回 `ERROR_INCOMPATIBLE_DRIVER` / `Found no drivers`；未执行 GPU kernel、渲染或 Vulkan 设备级运行 |
| 最终全仓库测试、Vulkan/WSL 定向 race、20 次乱序与 go vet | **通过** |

新增 `TestRealVulkanNativeBuildAndIncrementalDependencies` 可在 Linux/WSL 内启用：

```sh
TRESTLE_TEST_VULKAN=1 TRESTLE_TEST_VULKAN_ROOT=/usr go test -count=1 -v -timeout=5m ./internal/app -run TestRealVulkanNativeBuildAndIncrementalDependencies
```

Windows 驱动 WSL 的测试另行启用：

```powershell
$env:TRESTLE_TEST_WSL_VULKAN = '1'
$env:TRESTLE_WSL_DISTRIBUTION = 'ArchLinux'
go test -count=1 -v -timeout=10m ./internal/app -run TestRealWSLVulkanBuildAndIncrementalDependencies
```

本次 WSL 内验证使用 Windows Go 交叉构建的 Linux 测试二进制，直接在 ArchLinux 执行；与 Windows Go 再创建 WSL 子进程的执行方式有区别。相关日志为 `.trestle/vulkan-linux-pass.log`、`vulkan-device.log`、`vulkan-probe.log`、`vulkan-final-all.log`、`vulkan-final-cover.out`、`vulkan-race-final.log`、`vulkan-stress-final.log`、`vulkan-vet.log`。

CI 已增加 Linux 上两种真实 shader 编译器与 SPIR-V 验证工具，并新增手动 WSL Vulkan 工作流；本次没有触发远程 CI。

## CI 跨平台失败修正

根据 macOS Actions 的实际失败日志，`TestTranslateWSLShaderActionPathsPrecisely` 在 Unix 宿主仅转换了 include 参数，没有识别 `C:/...`。原因是使用了依赖宿主语法的 `filepath.VolumeName`。现在按 Windows 驱动器/UNC 语法识别参数，并按同样的跨平台规则识别绝对 `wsl.exe` 路径；Windows、Linux 都执行同一套断言，没有跳过 Unix 测试。

工作流补充了以下约束：

- actionlint 固定版本 1.7.7 作为前置检查，声明 CUDA/WSL Vulkan 自托管标签，检查表达式和内嵌脚本。发布说明中的变量引用问题已由 ShellCheck 检出并修复。
- 原生测试矩阵明确使用 Linux `g++`、Windows `clang-cl`、macOS `clang++`；检查编译器和 Ninja 后再运行真实构建。初始化中 `auto` 的多编译器告警不是这次测试退出的原因，真实原生构建使用显式选择。
- Ninja 优先使用 runner 已安装的版本；缺少时安装到临时 venv，避免修改全局 Python 环境，并分别处理 Windows Git Bash PATH 与 GITHUB_PATH。
- PR 执行质量门禁；手动运行执行检查和打包；只有 `main` push 能发布 release。日志上传工作流也仅处理 push 的正式发布，避免手动检查后查找不存在的 release。

修复后的本地验证：Windows 全仓库 `go test -count=1 -timeout=8m ./...`、Windows 20 次乱序 race 路径回归、`go vet ./...` 通过；使用 Go 1.26.0 交叉编译的全部 Linux 测试二进制在 Ubuntu-24.04 WSL 实际执行通过，g++ 原生程序与 Workspace 构建/运行通过；同一 Linux 路径回归执行 20 次通过；Linux 上 actionlint 与 ShellCheck 0.10.0 对所有工作流检查通过。

Linux 的独立 Go 1.26 工具链下载遇到 TLS 超时，因此该轮 Linux 测试使用本机 Go 1.26 交叉编译，测试中调用的 `go version` 等辅助命令使用 WSL 已安装的 Go。macOS 尚需 Actions 实际重跑确认，不能将交叉构建或 Linux 运行表述为 macOS 通过。日志保存在 `.trestle/ci-fix-windows.log` 和 `.trestle/ci-fix-linux.log`。
