# Build Analyzer 基准

## M5 原生 Ninja 配对实验

Windows 11 amd64，两边从 Ninja 同一提交 `a00b184d0cc3658ce2f5d2a81c7d86569d950b57` 编译，版本均为 `1.14.0.git`，使用 MSVC 14.44 Release。该提交的原生实现已经使用上一轮动作耗时。补丁只允许以同配置的历史中位数覆盖这一输入。

每次运行使用新的目录、同一份清单、同一份真实训练日志副本和相同的四个缺失输出，固定 `-j 2`。训练包括三个常规样本和一个异常的最后样本。六对测量交替两种执行顺序，验证四个完成边的动作身份和 Ninja 命令哈希完全一致，并检查关键边启动顺序发生变化。复测在其他测试完成后单独运行。

| 配对样本 | 原版墙钟 ms | 历史权重墙钟 ms |
| --- | ---: | ---: |
| 1 | 914 | 626 |
| 2 | 910 | 628 |
| 3 | 912 | 627 |
| 4 | 908 | 633 |
| 5 | 913 | 632 |
| 6 | 911 | 629 |
| 中位数 | 911.5 | 628.5 |

这是使用真实 Ninja 进程的受控合成实验。动作通过独立进程等待固定时长并写出目标，测试刻意模拟了最近一轮包含异常时长的情况。测量包含 Ninja 进程启动和执行开销，适用范围是该构造场景。

复现时让 `PATH` 优先解析同一提交构建的原版 Ninja，通过 `TRESTLE_HISTORY_NINJA` 指定补丁版，然后在空闲机器上运行：

```text
go test ./internal/buildanalyzer -run ^TestNativeNinjaHistoryExperiment$ -count=1 -v
```

## M7 分析器微基准

Intel Core Ultra 9 275HX、Windows amd64、Go 1.26.0。10,000 条边，12 个名额，受限池深度 2，执行两种离线策略。三次采样为 12.43、13.22 和 9.58 ms/op，约 1.77 MB/op。该指标只度量模拟器。

```text
go test ./internal/buildanalyzer -run ^$ -bench ^BenchmarkAnalyzerSimulation$ -benchtime=1s -count=3 -benchmem
```
