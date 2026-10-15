# GoRss 1.2.0

## 相比 1.1.1 的更新

- **字体层级**：细化导航、文章列表、正文和辅助信息的字号与字重。正文保留自选字体、字号和行距，列表与操作控件使用独立的界面字体。
- **三栏表面层次**：用同一暖灰色系区分订阅栏、文章列表和正文阅读区，优化选中状态与分隔线。
- **翻译分段切换**：「原文／译文／双语对照」采用一体式滑动分段控件，支持方向键、Home 和 End。
- **摘要展开动效**：摘要支持平滑展开、收起与箭头转动。当前文章已完成的摘要可以直接重新展开，生成中收起会取消请求。
- **操作反馈**：收藏后显示实心星标与选中底色；摘要和划词译文复制后短暂显示成功勾选；工具栏按钮增加轻微按压和颜色过渡。
- **减少动态效果**：跟随系统设置简化位移和展开动画，保留清楚的操作状态。

## Changes since 1.1.1

- Refined typography for navigation, article lists, reading content and metadata. Reading font preferences remain independent of interface controls.
- Added distinct warm surfaces for subscriptions, article lists and the reading pane, with quieter separators and selection states.
- Added a sliding segmented control for original, translated and bilingual views, with arrow-key, Home and End navigation.
- Added smooth summary expansion and collapse. Completed summaries reopen directly within the current article; collapsing an active summary cancels generation.
- Added filled star feedback, temporary copy-success checks for summaries and selection translations, and subtle toolbar press and color transitions.
- Respects the system Reduce Motion setting.
