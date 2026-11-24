# Agent 笔记：Git Log 提交列表采用 SourceGit 的呈现

状态：已实现

## 先说结论

Log 标签页的提交列表和提交图按 SourceGit 桌面客户端的外观呈现：SourceGit 的深浅两套内容色、分支/标签的描边标签、作者头像与 SHA 列、24 点列标题，以及它默认的十色 lane 调色板和圆点画法。图拓扑、提交顺序、颜色 ID 仍来自 Core 的投影；这次只换呈现层，不改 Core，也不改筛选、选择和工作树页面的行为。

## 问题

Viva 客户端的 Git Log 原先沿用 IntelliJ IDEA 的呈现：作者与时间挤在提交标题右侧，引用合并成右侧一组标签图标，lane 颜色由 Core 的颜色 ID 经 IDEA 的 hue 算法推出。用户要求把 Git Log 的样式对齐 SourceGit 桌面端，并让深色和浅色都按 SourceGit 的色板呈现。原先的 IDEA 基线无法同时满足“SourceGit 外观”和“可逐项核对颜色来源”。

## 决策

- 颜色取 SourceGit 仓库 `src/Resources/Themes.axaml` 的 `Color.Contents`、`Color.Border0`、`Color.Border1`、`Color.Border2`、`Color.FG1`、`Color.FG2` 与 `Brush.Accent` 语义；提交列表的选中行用 SourceGit 截图中的中性选中底色，而不是 IDEA 的蓝色选中，避免和整窗其他列表抢注意力。
- 列表行高保持 26 点，列标题 24 点。行内从左到右为“图 + 引用标签 + 主题”“作者（含 16 点头像）”“短 SHA（等宽）”“作者时间”。列标题由同一行宽在画布上直接绘制，首帧就和下面的列对齐；列表窄于 620 点时先收起作者时间与 SHA 列，窄于 470 点时再收起作者列，把宽度还给引用名称和主题，作者时间仍可在提交详情里看到。SourceGit 让用户手动隐藏列，这里用阈值自动收起。
- 引用标签照 SourceGit 的 `CommitRefsPresenter` 画：16 点高、4 点圆角、1 点描边，左侧 16 点图标槽，右侧标签底色是该引用所属 lane 颜色的 20%；当前分支和 HEAD 用 60% 的 lane 颜色填充并加粗。主标签最多的顺序是 HEAD/当前分支、本地分支、远程分支、标签。指向当前分支的 `HEAD` 不再重复画，远程的 `origin/HEAD` 这类符号引用不画。
- 引用名称优先于提交主题：标签先用整行可用宽度完整排布，只有整个提交列都放不下时才省略；主题在标签之后按剩余宽度截断。标签文字矩形按实际测得宽度绘制，右侧留 4 点，不因给主题预留空间而提前省略。
- 与在检分支无关的提交整行按 65% 不透明度绘制，当前 HEAD 所在行整体加粗，和 SourceGit 的 `IsMergedToOpacity` 与 `IsCurrentHead` 行为对应。
- 提交图使用 SourceGit 默认的 `Orange`、`ForestGreen`、`Turquoise`、`Olive`、`Magenta`、`Red`、`Khaki`、`Lime`、`RoyalBlue`、`Teal` 调色板，线宽 2 点、lane 间距 12 点。Core 的颜色 ID 是引用名的稳定哈希，不是调色板下标，所以在绘制前按图首次出现的顺序把 ID 映射到调色板；映射缓存在投影代次上，不改变 Core 的 ID。
- 分列由 Core 的 `laneMode: "stable"` 提供：每条分支线从分叉到合并、根提交或未加载父提交为止固定占用一列，分支结束后列才被后来的分叉复用；合并时按永久 layout index 选择继续主线，其他父提交迁出到新列。宿主只按 Core 返回的 lane 绘制，不再自行压缩或重排。
- 换道边不用折线：每行只画整行三次贝塞尔曲线的一半，相邻两行在行边界以相同切线相接，提交中心处切线垂直。这样分支跨行时是一条平滑曲线，不会出现折角或多分支挤在一起时难以辨认的菱形。
- 圆点照 SourceGit 的 `CommitGraph.DrawAnchors` 画：普通提交是内容色填充、lane 色 2 点描边的小圆；HEAD 是 12 点外圈加 6 点实心内点；合并提交是 12 点实心加内容色十字。点内部的颜色固定用内容色，与 SourceGit 的 `DotBrush` 一致。
- 提交列表背景改为 SourceGit 的内容色；列标题下的分隔线用 `Border0`，宽度 1 点。

## 考虑过的备选方案

- **只换色板、保留 IDEA 行布局**：改动最小，但引用仍挤在右侧，作者、SHA 和时间没有分列，和 SourceGit 截图一眼就能看出不同，因此没有采用。
- **让 SourceGit 的 lane 颜色继续由 Core 的哈希颜色 ID 取模**：实现更少，但同一张图里相邻分支容易撞色，而且与 SourceGit 的“按图路径顺序取色”不一致，因此改为按首次出现顺序映射。
- **把列标题、行布局也搬进 Core**：列宽、字号、是否显示列都是呈现状态，Core 不应保存；Core 继续只给稳定的行、引用和颜色 ID。
- **照搬 SourceGit 的用户可拖动列宽与相对时间**：列宽和相对时间格式属于交互与内容格式，涉及设置和 Core 的时间文本规则，超出本次“列表与图样式”的范围，没有引入。

## 后果

- Log 标签页的深浅两套外观都能与 SourceGit 的截图逐项对照：内容色、描边标签、头像、SHA 列、列标题和图例颜色。
- Worktrees 标签页的 History 列表复用同一套 lane 调色板与圆点绘制，因此它的图也会变成 SourceGit 风格；它的文字列布局保持原样。这是共享绘制代码的代价，不影响 Log 的列规则。
- 列表变窄时自动收起作者和 SHA 列，SourceGit 的用户可拖动列宽不再适用；需要精确列宽时以后再加设置，不能把列宽写死回单一宽度。
- 选中行、悬停行和当前分支的强调色都只作用于提交列表，不改变整窗其他列表、Diff 或 Commit 面板。把 Git Log 的新颜色推广到其他面板需要单独的决策。

## 验证

- `scripts/desktop/desktop-go.sh test ./internal/workbench`：Log 的菜单、选择、筛选、分页、多仓库与详情测试全部通过。
- `scripts/desktop/desktop-go.sh test -tags "vivahead lithevisual" ./internal/workbench -run 'TestVisualWorkbench/.*/50-git-log$'` 的 `50-git-log` 场景在深色和浅色下截图，核对列标题、描边标签、头像、SHA 与 lane 颜色；截图只用于本次验收，不提交到仓库，也不作为像素基线。
- `node scripts/i18n/generate-i18n.mjs --check`：四个列标题的中英词条和生成结果一致。

## 适用范围

- `desktop/internal/workbench/gitlog_style.go`（新增：色板、列布局、标签、头像、lane 颜色映射）
- `desktop/internal/workbench/gitgraph.go`
- `desktop/internal/workbench/gitlog_view.go`
- `desktop/internal/workbench/gitlog_page.go`
- `desktop/internal/workbench/gitworktree_view.go`、`gitworktree.go`（共享 lane 颜色缓存与圆点绘制）
- `desktop/internal/i18n/messages.go`、`en.go`、`zh.go` 与 `shared/i18n/` 中的列标题词条
- 参考实现固定为 [sourcegit-org/sourcegit](https://github.com/sourcegit-org/sourcegit) `master` 的 `src/Resources/Themes.axaml`、`src/Resources/Styles.axaml`、`src/Views/Histories.axaml`、`src/Views/CommitGraph.cs`、`src/Views/CommitRefsPresenter.cs` 和 `src/Models/CommitGraph.cs`；上游改变这些值时，先核对再更新本 Note 与色板。
