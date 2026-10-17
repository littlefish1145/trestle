# `trestle.toml` 配置参考

[English](configuration.en.md) · [返回 README](../README.md)

`trestle.toml` 是 Trestle 项目的事实来源：它描述项目身份、构建与工具链设置、依赖提供方、目标、条件规则和任务。Trestle 读取它来生成 `build.ninja`、`compile_commands.json` 和命令元数据；这些产物可以随时删除重建，配置本身适合纳入版本控制。

## 加载、发现与保存

- **发现顺序**：在项目目录中先查找 `trestle.toml`；没有时查找唯一的一个一级子目录中的同名文件；若存在 `CMakeLists.txt` 则回退到当前目录的默认文件名；否则提示先执行 `trestle init`。
- **未知字段会报错**：无法识别的键会以 `unknown configuration fields: ...` 拒绝加载，避免拼写错误被静默忽略。
- **schema 版本**：当前为 `7`（`CurrentSchemaVersion`）。文件版本更高会被拒绝（`E_CONFIG_NEWER_SCHEMA`）；更低会在内存中迁移到当前版本，并在**保存**时把未被改动的原文件备份为 `trestle.toml.schema-v<旧版本>.<指纹前 12 位>.bak`。
- **并发编辑检测**：保存前比较文件指纹，若磁盘内容与加载时不一致则报 `E_CONFIG_CONFLICT`，并保留当前文件。
- **路径解析**：项目内的相对路径以 `trestle.toml` 所在目录为基准；绝对路径原样保留。保存时项目内路径会写回为相对路径，便于 `trestle init -C 相对目录` 或移动检出目录后不产生重复前缀。

## 顶层结构

```toml
schema_version = 7

[project]          # 项目身份
[build]            # 全局构建设置
[toolchain]        # 工具链选择
[compiler_presets] # 可复用的编译器预设
[vcpkg]            # vcpkg 提供方
[conan]            # Conan 2 提供方
[targets.<名称>]    # 可构建单元
[packages.<名称>]   # 依赖包声明
[package]          # 发布归档设置
[[rules]]          # 条件规则
[tasks.<名称>]      # 显式任务
```

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `schema_version` | 整数 | `7` | 必须等于当前支持的版本。 |

## `[project]`

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `name` | 字符串 | `trestle-project` | **必填且非空**。用于种子化默认输出名与默认目标。 |

## `[build]`

对每个目标生效的全局设置，除非被规则或目标覆盖。

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `profile` | 字符串 | `debug` | 构建配置，通常为 `debug` 或 `release`。 |
| `build_dir` | 字符串 | `build` | 生成文件与产物的目录。 |
| `c_standard` | 字符串 | `c17` | C 语言标准。 |
| `cxx_standard` | 字符串 | `c++20` | C++ 语言标准。 |
| `compile_flags` | 字符串数组 | `[]` | 所有语言通用的编译参数。 |
| `c_flags` | 字符串数组 | `[]` | 仅 C 的编译参数。 |
| `cxx_flags` | 字符串数组 | `[]` | 仅 C++ 的编译参数。 |
| `link_flags` | 字符串数组 | `[]` | 链接参数。 |
| `default_targets` | 字符串数组 | 自动选择 | 未指定目标时构建的目标。留空时依次优先：项目名、`app`、排序后的可执行目标、排序后的首个目标。 |
| `compile_commands` | 字符串 | `compile_commands.json` | `compile_commands.json` 的输出路径。 |
| `auto_compile_shaders` | 布尔 | `true` | 是否自动编译 Vulkan Shader 目标。 |
| `modules` | 布尔 | `false` | 是否启用 C++ 模块扫描。 |
| `module_scanner` | 字符串 | 空 | 保留字段，当前未使用；请改用 `[toolchain].module_scanner`。 |

## `[toolchain]`

工具链按**可移植的版本约束**选择，而不是记录某台机器的绝对路径。版本选择器语法：

- `auto`：使用最新安装的版本（`c`、`cxx`、`msvc` 的默认值）。
- 点号版本：如 `14.30`，固定到某个版本。
- 区间：如 `14.30~14.50`，两端闭合。
- 裸可执行名：如 `clang++`。
- 本机绝对路径：逃生舱，Trestle 自身从不写入。

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `mode` | 字符串 | `native` | `native` 或 `wsl`。 |
| `c` / `cxx` | 字符串 | `auto` | C / C++ 编译器选择器。 |
| `msvc` | 字符串 | `auto` | MSVC 工具集选择器。 |
| `archiver` | 字符串 | 空 | 归档器（`lib`/`ar`）覆盖。 |
| `linker` | 字符串 | 空 | 链接器覆盖。 |
| `setup` | 字符串 | 空 | `vcvars64.bat` 或环境设置脚本。 |
| `cuda` | 字符串 | 空 | CUDA 版本选择器（如 `12.0~12.9`）。 |
| `cuda_root` | 字符串 | 空 | CUDA 根目录覆盖。 |
| `cuda_mode` | 字符串 | `whole` | `whole` 或 `rdc`。 |
| `cuda_architectures` | 字符串数组 | `["sm_75"]` | 目标 GPU 架构。 |
| `cuda_execution` | 字符串 | `native` | CUDA 在 `native` 还是 `wsl` 中执行。 |
| `cuda_wsl_distribution` | 字符串 | 空 | 承载 CUDA 的 WSL 发行版。 |
| `vulkan` | 字符串 | 空 | Vulkan SDK 根目录。 |
| `vulkan_execution` | 字符串 | `native` | Vulkan Shader 工具在 `native` 还是 `wsl` 中执行。 |
| `vulkan_wsl_distribution` | 字符串 | 空 | 承载 Vulkan SDK 工具的 WSL 发行版。 |
| `module_scanner` | 字符串 | 空 | `clang-scan-deps` 可执行文件。 |
| `cache_dir` | 字符串 | `.trestle/toolchain` | 工具链探测缓存目录，也可用环境变量 `TRESTLE_TOOLCHAIN_CACHE`。 |
| `wsl_distribution` | 字符串 | 空 | `mode = "wsl"` 时使用的发行版。 |
| `preset` | 字符串 | 空 | 引用的编译器预设名。 |

## `[compiler_presets.<名称>]`

具名、可复用的工具链选择。规则和 `package.optimization = "custom:<预设>"` 可引用它，替换重复的编译器与参数设置。空 `mode` 表示继承项目工具链。

| 键 | 类型 |
| --- | --- |
| `c` / `cxx` / `archiver` / `linker` / `setup` | 字符串 |
| `mode` | 字符串（`native` 或 `wsl`） |
| `wsl_distribution` | 字符串 |
| `compile_flags` / `c_flags` / `cxx_flags` / `link_flags` | 字符串数组 |

## `[vcpkg]`

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `root` | 字符串 | `VCPKG_ROOT` | vcpkg 安装根目录。 |
| `triplet` | 字符串 | `auto` | vcpkg triplet；`auto` 时按目标架构选择。 |
| `crt_linkage` | 字符串 | 空 | `static` 或 `dynamic`，用于匹配 triplet。 |
| `library_linkage` | 字符串 | 空 | `static` 或 `dynamic`，用于匹配 triplet。 |

## `[conan]`

供 `provider = "conan"` 的包使用。

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `root` | 字符串 | 空 | Conan 缓存（home）目录。 |
| `build_root` | 字符串 | 构建目录的父目录 | 每个 profile 的 `conan-<profile>` 安装输出父目录。 |
| `profile` | 字符串 | `default` | `conan profile detect` 使用的 profile。 |
| `settings` | 表 | 空 | 每次安装以 `-s` 传入，保持交叉构建可复现。 |
| `options` | 表 | 空 | 每次安装以 `-o` 传入。 |

## `[targets.<名称>]`

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `type` | 字符串 | 必填 | `static`、`shared`、`executable`、`test`、`shader`。 |
| `sources` | 字符串数组 | 必填，非空 | 源文件通配模式，如 `src/*.cpp`。 |
| `output_name` | 字符串 | 目标名 | 产物文件名。 |
| `when` | 字符串 | 空 | 条件表达式，为假时目标不可用。 |
| `requires_tools` | 字符串数组 | `[]` | 构建前必须存在的工具。 |
| `requires_wsl` | 字符串 | 空 | 必须存在的 WSL 发行版。 |
| `c_standard` / `cxx_standard` | 字符串 | 空 | 覆盖全局语言标准。 |
| `export_all_symbols` | 布尔 | `false` | 共享库导出全部符号。 |
| `include_dirs` | 字符串数组 | `[]` | 公开头文件目录（向依赖者传播）。 |
| `private_include_dirs` | 字符串数组 | `[]` | 仅本目标使用的头文件目录。 |
| `defines` | 字符串数组 | `[]` | 公开宏定义。 |
| `private_defines` | 字符串数组 | `[]` | 私有宏定义。 |
| `compile_options` | 字符串数组 | `[]` | 编译选项。 |
| `c_flags` / `cxx_flags` | 字符串数组 | `[]` | 仅 C / 仅 C++ 的编译参数。 |
| `library_dirs` | 字符串数组 | `[]` | 库目录。 |
| `libraries` | 字符串数组 | `[]` | 链接的库。 |
| `link_options` | 字符串数组 | `[]` | 链接选项。 |
| `interface_compile_options` | 字符串数组 | `[]` | 仅向依赖者传播的编译选项。 |
| `interface_link_options` | 字符串数组 | `[]` | 仅向依赖者传播的链接选项。 |
| `shader_stage` | 字符串 | 空 | `shader` 类型**必填**：`vert`、`frag`、`comp`、`geom`、`tesc`、`tese`。 |
| `shader_entry` | 字符串 | 空 | Shader 入口点名称。 |
| `shader_tool` | 字符串 | 空 | Shader 编译工具（如 `glslc`）。 |
| `shader_target_env` | 字符串 | 空 | 目标环境（如 `vulkan1.1`）。 |
| `dependencies` | 表数组 | `[]` | 依赖边，见下。 |
| `test_group` | 字符串 | 空 | 测试分组名，供 `trestle test --group` 使用。 |
| `test_args` | 字符串数组 | `[]` | 运行测试时传入的参数。 |
| `test_working_dir` | 字符串 | 空 | 测试运行的工作目录。 |

### `[[targets.<名称>.dependencies]]`

| 键 | 类型 | 说明 |
| --- | --- | --- |
| `target` | 字符串 | 依赖的项目目标。`target` 与 `package` **必须且只能设置一个**。 |
| `package` | 字符串 | 依赖的已声明包。 |
| `scope` | 字符串 | `private`（默认）、`public` 或 `interface`。 |

## `[packages.<名称>]`

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `provider` | 字符串 | `vcpkg` | 空、`vcpkg`、`conan` 或 `manual`。 |
| `port` | 字符串 | 键名 | 上游包名。 |
| `version` | 字符串 | 空 | Conan 版本；features 属于引用名而非版本。 |
| `features` | 字符串数组 | `[]` | 启用的包特性。 |
| `triplet` | 字符串 | 空 | 覆盖 vcpkg triplet。 |
| `include_dirs` / `library_dirs` / `libraries` / `defines` | 字符串数组 | `[]` | 手动（`manual`）用法路径。 |
| `runtime_files` | 字符串数组 | `[]` | 需要随产物部署的运行时文件（如 DLL）。 |

## `[package]`

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `format` | 字符串 | `zip` | 目前仅支持 `zip`。 |
| `output` | 字符串 | `dist/<项目名>.zip` | 归档输出路径。 |
| `optimization` | 字符串 | `balanced` | `balanced`、`speed`、`size` 或 `custom:<预设>`。 |
| `targets` | 字符串数组 | `build.default_targets` | 打包的目标。 |

## `[[rules]]`

规则按 TOML 顺序应用；条件为假时不改变配置。

| 键 | 类型 | 说明 |
| --- | --- | --- |
| `when` | 字符串 | **必填**，条件表达式。 |
| `target` | 字符串 | 设置后，以下 flags 与依赖只影响该目标。 |
| `c` / `cxx` | 字符串 | 设置编译器（项目级）。 |
| `mode` | 字符串 | `native` 或 `wsl`（项目级）。 |
| `wsl_distribution` | 字符串 | WSL 发行版（项目级）。 |
| `compile_flags` / `c_flags` / `cxx_flags` / `link_flags` | 字符串数组 | 视是否设置 `target` 而定作用范围。 |
| `packages` | 字符串数组 | 为 `target` 添加外部包依赖；**需要 `target`**。 |
| `depends_on` | 字符串数组 | 为 `target` 添加项目目标依赖；**需要 `target`**。 |
| `targets` | 字符串数组 | 设置默认目标（项目级）。 |

引用的目标、包与依赖名都必须已声明。

## `[tasks.<名称>]`

任务是一条需显式确认的 argv 命令，其后跟随白名单化的配置修改。

| 键 | 类型 | 说明 |
| --- | --- | --- |
| `description` | 字符串 | 任务说明。 |
| `command` | 字符串数组 | 以 argv 形式执行，不经 shell。 |
| `working_dir` | 字符串 | 项目相对或绝对路径；默认项目目录。 |
| `timeout` | 字符串 | 正时长，如 `30s`、`5m`；省略则不限制。 |
| `set` | 表 | 任务成功后应用的配置修改；仅允许白名单键。 |

`set` 允许的键：`build.profile`、`toolchain.c`、`toolchain.cxx`、`toolchain.msvc`、`toolchain.mode`、`toolchain.wsl_distribution`、`vcpkg.root`、`vcpkg.triplet`、`conan.root`、`conan.profile`。

## 条件表达式

`when` 使用刻意精简、无副作用的表达式：

- 运算：`&&`、`||`、`!`、`==`、`!=`、括号。
- 变量：`os`、`arch`、`mode`（均为字符串）。
- 只读函数（返回布尔，`env` 返回字符串）：
  - `tool("名称")`：当前模式下查找工具；WSL 模式在选定发行版中查找。
  - `wsl("发行版")`：发行版是否存在。
  - `wsl_tool("发行版", "工具")`：某发行版中是否存在工具。
  - `pkg("包名")`：声明的包能否解析。
  - `env("变量")`：读取环境变量。
  - `path("路径")`：路径是否存在；相对路径以 `trestle.toml` 所在目录为基准。

```toml
[[rules]]
when = 'os == "windows" && wsl_tool("Ubuntu", "clang++")'
mode = "wsl"
wsl_distribution = "Ubuntu"
cxx = "clang++"
```

## 校验与错误代码

| 代码 | 含义 |
| --- | --- |
| `E_CONFIG_NEWER_SCHEMA` | 文件要求的 schema 高于当前可执行文件。 |
| `E_CONFIG_OLD_SCHEMA` | schema 过低且无法迁移。 |
| `E_CONFIG_MISSING_REQUIRED` | 缺少 `project.name`，或没有任何目标。 |
| `E_CONFIG_INVALID_VERSION` | `[toolchain]` 版本选择器无法解析。 |
| `E_CONFIG_CONFLICT` | 保存前检测到并发编辑。 |
| `E_CONFIG_BACKUP` | 无法把迁移前的原文件备份到旁边。 |
| `E_DEP_CYCLE` | 目标依赖存在环。 |

此外，`: unknown configuration fields: ...` 表示存在未识别的键。

## 另见

- [命令行参考](cli.zh-CN.md)
- [从零构建带 fmt 依赖的 C++ 项目](tutorial.zh-CN.md)
