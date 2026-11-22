# Build Analyzer 实现状态

基线审计使用 `ebac4f6` 上已有 28 项本地改动的工作区。仓库及检查的父目录未发现 AGENTS.md。Windows/amd64 使用 Go 1.26.0 和 Ninja 1.13.2，已有构建日志是 v7。初始 `go test ./...` 通过。

## M1 已验证

- `internal/buildanalyzer/log.go` 和 `session.go` 负责日志增量与 JSON 会话。CLI 和 app 接入原有构建流程。
- Windows 真实 CLI 测试使用 Clang 和 Ninja，覆盖中文及空格路径、首次构建、无任务构建、编译失败和两种 profile 语义。日志单元测试覆盖 CRLF、截断和前缀替换。
- 外部 CMake 工作区报告不支持。日志发生压缩时，当前会话明确标为不完整。

## M2 已验证

- `graph.go`、`dependencies.go`、`dyndep.go`、`timing.go` 和 `trace.go` 构建动作图和指标。时序按生产动作归并，多输出只计一次，phony 与未观测到的边权重为零。调度依赖保留 order-only。
- `go test ./internal/buildanalyzer ./internal/ninja ./internal/plan -count=1` 通过。真实 Ninja fixture 写入并加载 dyndep，覆盖多输出、隐式输入、只保序输入、depfile、phony 和 pool。另验证依赖环、最长路径和并发计数。
- 图的动态输入来自执行后的 Ninja 依赖状态。尚未产生或已过期的依赖元数据会使图标为不完整。

## M3 已验证

- `analyze impact` 使用显式、隐式和编译器发现的依赖，支持跨成员文件传播。只保序依赖不传播脏状态。`analyze why` 查询真实 Ninja 调用的解释。
- Windows 真实 CLI 测试覆盖头文件影响到链接、头文件编辑后的重编译，以及命令修改解释。查询单元测试覆盖跨工作区成员、只保序边及日志分块。

## M4 已验证

- 会话报告作为样本来源，按项目路径、解析后的配置、C/C++、CUDA 和 Vulkan 工具链指纹、动作输出及 Ninja 命令哈希匹配。至少三个样本才生成中位数预测。
- 损坏报告、跨 profile、跨工具链、命令哈希隔离、异常值及资源约束测试通过。真实 CLI 重复修改同一头文件，报告生成了预测、误差与调度模拟。
- 模拟要求显式 `--jobs N`。使用相同的已执行动作和实际耗时进行离线比较，不计作调度性能实测。

## M5 已验证

- `scheduler.go` 与 `ninja-a00b184-history.patch` 接通实验后端。本机会话通过真正的补丁 Ninja 获得了已应用历史权重的回执。
- 同一上游提交的原版和补丁版各运行六次。每组脏动作集合及命令哈希相同，观察到调度启动顺序变化。详细值和复现方式见 [benchmarks.md](benchmarks.md)。
- Ninja 1.13.2 发布标签仍以固定权重调度非 phony 边；实验因此改用已在调度中采纳最近耗时的上游提交。普适性能收益未量化。

## M6 已验证

- `deep.go` 接入 Clang JSON、GCC 阶段墙钟、MSVC 前后端阶段和 NVCC CSV。深度构建使用独立输出和编译数据库。
- Windows 真实 CLI 测试分别用 Clang 18.1.8、WSL Debian GCC 14.2、MSVC 14.44 和 CUDA 13.2 NVCC 构建并生成事件。测试逐字节比对了普通清单和根目录编译数据库。
- 真机测试还通过了原生 Trestle 双成员工作区的深度构建。两个成员均生成编译器计时，链接所用的成员产物全在会话目录中。
- 外部 CMake 工作区沿用自己的构建入口。MSVC 适配器采集编译器阶段时间，ETW Build Insights 不属于当前输出。

## M7 已验证

- `internal/tui/build_analyzer.go` 接入命令面板、构建动作和报告页。`analyze html` 导出可脱网查看的报告。`report.html` 模板对项目路径和诊断做 HTML 转义。
- TUI 命令和视图验证、离线 HTML 内容及转义测试通过。10,000 边模拟基准已运行，记录见 [benchmarks.md](benchmarks.md)。
- 最终 Windows 回归通过 `go test -p 4 ./...`、`go vet -p 4 ./...` 和单独输出路径的 CLI 编译。真实 CLI 复测覆盖 Trace JSON、HTML、失败会话和实验调度器的权重回执。
