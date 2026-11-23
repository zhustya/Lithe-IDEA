# Agent 笔记：Git Graph 改为原生 plugin view

状态：已实现

## 先说结论

新增独立插件 `Plugins/git-graph-native/`，用 Lithe 的 plugin view（原生 canvas + 宿主控件）绘制提交图，不使用 WebView。web 版在 Windows 上关闭工具窗口后 WebView 残留（宿主/Viva 生命周期问题），原生版由宿主直接绘制，关闭行为正常。

## 决策

- 清单只用 `ui` 的 `view` 工具窗口，不声明 `ui.web`；后端仍是 `systemCommand node`。
- 后端通过 `view()` 注册一个函数处理 `open`/`close`/`change`/`click`/`resize`，状态按 `event.instance` 保存。
- 提交图用 canvas 的 `path`（合并连线）、`rect`（圆点和 refs 徽标）、`text` 和带 `on:['click']` 的 group（可点击行）；颜色用 `$accent`/`$error`/`$warning`/`$selection` 等主题 token。
- 仓库发现与 git log/show 逻辑复用 web 版的只读实现；原生 canvas 不能打开文件，改动文件只显示列表。

## 验证

- `node --check bin/backend.mjs`、`node --check package.mjs`
- 清单的 requires/permissions 按 Core schema 核对通过
- ZIP 完整性校验通过，路径均为 `/`

## 适用范围

- `Plugins/git-graph-native/`
