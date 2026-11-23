# Git Graph Lite (read-only)

Lithe 的只读 Git 提交图工具窗口。**从头编写，不复用 vscode-git-graph 的代码**，因此不受其"禁止分发衍生作品"的许可证限制。

## 功能（第一版）

- 自动列出当前工作区（含其子目录）里的 Git 仓库
- 提交图：分支泳道、合并连线、HEAD/分支/远程/标签标记
- 搜索提交（subject / author / hash）
- 点击提交查看完整提交信息与改动文件；点击文件在 IDE 中打开
- 主题跟随 Lithe，仓库与视图状态可保存

只读：不提供 checkout / commit / push 等写操作。

## 架构

- `plugin.json`：声明式清单，web 工具窗口 + 插件后端（Node）+ `workspace.metadata`
- `bin/backend.mjs`：用 Lithe 官方 Node SDK（vendored，Apache-2.0）实现
  `graph.repos` / `graph.log` / `graph.commit` 三个函数；通过 `git` CLI 读取数据
- `ui/`：原生 HTML/CSS/JS，提交图用 SVG 绘制，通过 `window.lithe.request('functions.invoke', …)` 调用后端
- Git 是 Lithe 内置模块，不向插件开放状态 API，因此本插件自己运行 `git` 命令（只读）

## 打包与安装

```
node package.mjs        # 生成 git-graph-lite-0.1.0.zip
```
IDE → 插件 → 「从磁盘安装插件…」→ 选择 zip → 确认权限 → 重启 IDE → 右侧工具条打开 "Git Graph"。

前置条件：Node 20.3+、git 在 PATH 中。

## 文件

- `plugin.json`
- `bin/backend.mjs`
- `ui/index.html`, `ui/app.js`, `ui/style.css`
- `vendor/lithe-plugin-sdk/`（来自 Lithe-core-rust 官方 SDK）
