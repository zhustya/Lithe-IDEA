# Git Graph (Native View)

Git Graph 的原生版本：用 Lithe 的 **plugin view** 绘制，**不使用 WebView**，因此不存在工具窗口关闭后网页残留的问题。

## 功能

- 自动发现工作区里的 Git 仓库（根目录及一层子目录）
- 原生 canvas 绘制提交图：分支泳道、合并连线、refs 标签
- 仓库/数量选择、搜索框、刷新按钮（都是宿主原生控件）
- 点击提交查看详细信息、作者、日期和改动文件

只读插件，不提供 checkout/commit/push。

## 注意

- 原生 view 的 canvas 不能打开文件，改动文件只显示列表（web 版支持点击跳转）
- 需要 `node`（从 PATH 启动后端）和 `git`
- 清单不需要 `ui.web`，只用 `ui.view`

## 打包

```
node package.mjs        # 生成 git-graph-native-0.1.0.zip
```
