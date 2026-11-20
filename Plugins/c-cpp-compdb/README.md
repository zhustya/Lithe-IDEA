# C/C++ Support (auto compilation database)

一个**纯声明式** C/C++ 语言插件：clangd 启动前自动生成工作区的 `compile_commands.json`，
之后 clangd 自己读取它，跳转 / 补全 / 引用即可正常工作。宿主与 Core 都不需要改动。

## 原理

1. 插件把语言服务器声明为 `node ${pluginDir}/clangd-wrapper.mjs`（`systemCommand`）。
2. `clangd-wrapper.mjs` 在收到 LSP `initialize` 时，从 `rootUri` / `workspaceFolders` 得到工作区根目录。
3. 它调用 `generate-compdb.mjs`：
   - **CMake**：对每个含 `CMakeLists.txt` 的目录执行 `cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON`，
     把生成的数据库复制到该目录根部。
   - **Eclipse / MRS makefile**：解析 `obj/**/subdir.mk` 的 `C_SRCS` 与编译规则，
     提取 `-I` / `-D` / `-std`，把失效的临时路径映射回当前工程目录，
     生成每个子项目的 `compile_commands.json`（clangd 会从源文件向上自动找最近的一份）。
4. 包装器把缓存的 `initialize` 消息原样转发给真正的 clangd，之后透明代理 stdio。

不会再覆盖已经存在的 `compile_commands.json`。

## 前置条件

- `node`（用于运行包装器，Lithe 会从 PATH 发现）
- `clangd`（优先 PATH；找不到时会尝试 LLVM 的常见安装路径）
- CMake 生成路径需要本机装 `cmake`

## 安装

```
node package.mjs          # 生成 c-cpp-compdb-0.1.0.zip
```
在 IDE 里用「从磁盘安装插件…」安装该 zip，确认权限后重启 IDE。

## 验证

```
node --test tests/generate-compdb.test.mjs
```
或者直接对工程自检：
```
clangd --check=<某个 .c 文件>
```
应输出 `Loaded compilation database from .../compile_commands.json` 且 `0 errors`。

## 边界

- CMake 配置会执行 `CMakeLists.txt`（属于项目代码），请在可信工作区使用。
- 生成是启动时一次性的；构建配置变化后需重启语言服务器并删除旧的 `compile_commands.json`。
- 只为没有数据库的目录生成；已有数据库始终保持不动。
