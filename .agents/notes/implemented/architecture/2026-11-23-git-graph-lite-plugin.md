# Agent 笔记：只读 Git 提交图插件

状态：已实现

## 先说结论

新增声明式插件 `Plugins/git-graph-lite/`，提供只读的 Git 提交图工具窗口：仓库选择、分支泳道图、refs 标记、搜索、提交详情与文件定位。代码从头编写，不移植 vscode-git-graph（其许可证禁止分发衍生作品）。

## 问题

内置 Git Log 的分支图在复杂历史上可读性不足；VS Code 的 Git Graph 扩展不能安装到 Lithe（无 VSIX / `vscode` API 兼容层），且其 LICENSE 明确禁止分发衍生作品。

## 决策

- 插件由三部分组成：web 工具窗口（`ui.web`）、Node 后端（官方 SDK，vendored）、三个插件函数。
- 后端通过 `workspace.metadata` 取得工作区根，扫描根目录及一层子目录中的 `.git`，再用只读 `git log` / `git show` 取得数据；不依赖 Lithe 内置 Git 状态（插件无该公共 API）。
- web 页面用 SVG 绘制泳道图与合并连线；通过 `window.lithe.request('functions.invoke', …)` 调后端，通过 `workspace.openFile` 打开改动文件；主题与状态走 bridge。
- 打包用纯 Node 的 ZIP 写入器，条目名统一 `/`，避免 Windows 上 `Compress-Archive` 产生反斜杠路径。

## 考虑过的备选方案

- 移植 vscode-git-graph：需要重写全部 `vscode.*` 胶水，且许可证禁止分发衍生作品，否决。
- 改内置 Git Log 的分支树：需要改宿主并重编译 IDE；本插件可独立安装迭代，先做插件。

## 后果

- 只读：没有 checkout / commit / push，避免与内置 Git 的信任与状态边界冲突。
- 需要 Node 20.3+ 与 git 在 PATH；后端是每插件一个进程，工作区通过 context 显式传入。
- 多仓库工作区在下拉框里选仓库；状态保存在 `ui.setState`。

## 验证

- `node --check bin/backend.mjs`、`node --check ui/app.js`
- 用真实 `D:\hm\PUS\PUS_master` 验证 `git log` 记录分隔符与字段解析（3 条记录、6 字段）
- `node package.mjs` 生成 zip，`unzip -t` 全部通过，条目名均为 `/`

## 适用范围

- `Plugins/git-graph-lite/`
