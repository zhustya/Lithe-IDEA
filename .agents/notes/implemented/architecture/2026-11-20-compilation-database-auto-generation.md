# Agent 笔记：C/C++ 编译数据库自动生成并接入 clangd

状态：已实现

## 先说结论

C/C++ 语言插件在 LSP `initialize` 时读取工作区根目录，自动为工作区内的 CMake 与 Eclipse/MRS makefile 子项目生成 `compile_commands.json`，随后把已缓存的 initialize 消息转发给真正的 clangd。宿主与 Rust Core 都不需要改动：现有声明式插件契约的 `systemCommand` 启动和 `${pluginDir}` 模板已经足够。用户不再需要手工创建编译配置。

## 问题

clangd 靠 `compile_commands.json`（或 `compile_flags.txt`）取得每个 C/C++ 文件的 include 路径、宏和语言标准。Eclipse/MRS 工程没有标准编译数据库，makefile 里的 `-I` 还常指向已删除的临时目录，导致头文件找不到、AST 建不起来，跳转和引用全部返回空。Java/Go/Python 的构建元数据是标准化的，C/C++ 是例外。

## 决策

- 新增插件 `Plugins/c-cpp-compdb/`，语言服务器声明为 `node ${pluginDir}/clangd-wrapper.mjs`，`validationArguments` 用 `--version` 探测 node。
- `clangd-wrapper.mjs` 解析 LSP 帧，从 initialize 的 `rootUri`/`workspaceFolders` 取得工作区根（不依赖新的模板变量，也不需要改 Core 启动计划）。生成完成后原样转发缓存的字节流，并透明代理后续 stdio。
- `generate-compdb.mjs` 按目录扫描：
  - 含 `CMakeLists.txt` 且没有数据库的目录，执行 `cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON`，把结果复制到该目录根部。
  - 含 `.cproject` 和 `obj/` 的目录，解析 `obj/**/subdir.mk` 的 `C_SRCS` 与编译规则，提取 `-I`/`-D`/`-std`，把失效的绝对路径按“最长存在的相对后缀”映射回当前工程，写出每个子项目的数据库。
- 已有 `compile_commands.json` 的目录一律跳过，不覆盖用户配置。
- clangd 自己会从源文件向上寻找最近的数据库，所以包装器不传 `--compile-commands-dir`，多子项目工作区因此互不干扰。
- 生成出的命令去掉 `-march`/`-mabi`/`--param` 等代码生成参数（对宿主机 clang 无效且不影响 AST），保留 `-I`/`-D`/`-std`。

## 考虑过的备选方案

- **在 Core 加 `${workspaceRoot}` 模板变量并给 clangd 传 `--compile-commands-dir`**：需要改 Core 启动计划、契约文档、fixture 和宿主，而且解决不了“项目里没有数据库”这个真问题，否决。
- **在宿主里硬编码 CMake/MRS 生成逻辑**：违反领域知识只放在插件里的边界，否决。
- **要求用户手工生成**：这正是现状，否决。

## 后果

- 用户安装一个社区插件即可获得 C/C++ 跳转，不需要改 IDE 或 Core。
- CMake 配置会执行 `CMakeLists.txt`（项目代码），只应在可信工作区启用。
- 生成是一次性的；构建配置变化后要删除旧数据库并重启语言服务器。
-  packaging 用 `node package.mjs` 生成 zip，从磁盘安装。

## 验证

- `node --test Plugins/c-cpp-compdb/tests/generate-compdb.test.mjs`：MRS 规则解析、失效 include 修复、不覆盖已有数据库。
- 在真实 `D:\hm\PUS` 上生成 25 条命令；`clangd --check` 输出 `Loaded compilation database from .../compile_commands.json` 且 `0 errors`。
- 端到端向包装器发送 LSP initialize，确认数据库被生成且 clangd 正常返回 initialize 结果。

## 适用范围

- `Plugins/c-cpp-compdb/`
