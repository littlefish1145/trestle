# Build Analyzer 架构

CLI 位于 `cmd/trestle`。`internal/app` 解析配置并持有构建目录锁，`internal/plan` 生成动作，`internal/ninja` 生成清单，`internal/runner` 执行 Ninja。`internal/source` 提供源码扫描，现有 `trestle analyze` 使用该入口。`internal/tui` 通过服务回调调用 app。原始运行日志仍由 `internal/runlog` 管理。

分析器位于 `internal/buildanalyzer`。一次构建会话记录零次或多次 Ninja 调用，工作区成员各自有工作目录、配置和目标。日志采集发生在现有资源锁内，使用调用前的日志前缀摘要确认调用后的新增记录。会话目录位于项目的 `.trestle/build-analyzer/<会话>/`，包含通过原子替换发布的 `report.json`、`trace.json` 和轻量样本索引 `history.json`。`report.json` 最后发布，完成后可读取该会话。

构建配置继续接受 `build --profile debug`、`build --profile release` 和等号形式。裸 `--profile` 或 `--profile-build` 开启分析采集。位置参数前应写选项，名为 debug 或 release 的目标可用 `--` 分隔。

采集失败会产生诊断，构建退出结果仍由原构建流程决定。Ninja 日志被压缩、替换或存在无法完整读取的记录时，报告标识采集不完整。并行运行外部 Ninja 的进程不受 Trestle 资源锁保护。

## 数据流

- `session.go` 和 `log.go` 区分本次 Ninja 调用与已有日志。一个多输出命令在日志中可能产生多行，`timing.go` 按动作合并。失败会话保存错误和已记录的成功边。
- `graph.go` 在生成后的 `plan.BuildPlan` 上关联显式和隐式输出。执行后从 depfile、`.ninja_deps` 与 dyndep 补齐动态依赖。关键路径计算使用已观测到的耗时，phony 和未观测到的动作权重为零。Trace 的线程编号是由时间区间分配的显示轨道。
- `query.go` 的文件传播跨工作区成员，通过实际输出路径连接。order-only 参与调度先后顺序，文件变化经这类边不传播。 `why` 保存本次实际 Ninja 调用的 `-d explain` 输出。
- `history.go` 从最多 64 个有样本的已完成会话读取索引，同一配置、工具链和命令的单边最多取最近 15 个样本，达到三个后取中位数。模拟只用本次已执行任务集合，使用同样的观测耗时分别比较两种优先级，并执行显式设置的 jobs 与 pool 限制。
- `scheduler.go` 使用实验 Ninja 补丁在调度前输入中位数。补丁按展开后的原生命令哈希验配权重，沿用 Ninja 本身的关键路径算法、脏检查、缓存和执行器。默认 `--scheduler=ninja`。显式指定 history 时，缺少兼容后端或样本会走原生 Ninja；实际执行开始后，失败直接保留退出状态。
- `deep.go` 的 Clang、GCC、MSVC、NVCC 适配器只对 `--profile-deep` 生成的独立清单生效。GCC 和 MSVC 的包装器保持 stdout/stderr 可用于原有 Ninja 诊断和依赖提取。深度构建输出、编译数据库和原始采集文件保存在会话目录。

## 使用

```text
trestle build --profile --jobs 8
trestle analyze impact src/header.h
trestle analyze why
trestle analyze html
trestle build --profile-deep
trestle build --scheduler=history --jobs 8
```

`impact`、`why` 和 `html` 默认读取最新可读会话，`--report path/to/report.json` 可指定较早会话。 `html` 默认输出同目录的 `report.html`，支持 `--output`。Trace JSON 可交给支持 Chrome Trace Event 格式的查看器。TUI 的 `Ctrl+P` 命令面板提供 `Profile build` 和 `Open Build Analyzer`。深度构建每次使用独立目录，数据不进入常规构建的预测样本。

实验 Ninja 从上游提交 [a00b184d0cc3658ce2f5d2a81c7d86569d950b57](https://github.com/ninja-build/ninja/tree/a00b184d0cc3658ce2f5d2a81c7d86569d950b57) 构建。对单独的 Ninja 源码检出使用 `git apply <Trestle路径>/docs/build-analyzer/ninja-a00b184-history.patch`，按 Ninja 的 CMake 构建流程编译，并把可执行文件放入项目的 `.trestle/tools/build-analyzer/ninja-history.exe`，或用 `TRESTLE_HISTORY_NINJA` 指定绝对路径。实验数据和复现命令见 [benchmarks.md](benchmarks.md)。

Clang 使用 [时间追踪 JSON](https://clang.llvm.org/docs/UsersManual.html#options-to-emit-resource-consumption-reports)，GCC 使用 [`-ftime-report`](https://gcc.gnu.org/onlinedocs/gcc/Developer-Options.html)，MSVC 使用当前工具集的 `/Bt+` 阶段计时，NVCC 使用 [`--time` CSV](https://docs.nvidia.com/cuda/cuda-compiler-driver-nvcc/index.html#time-filename-time)。
