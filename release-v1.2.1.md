# GoRss 1.2.1

## 相比 1.1.1 的新增与优化

- **字体层级**：细化导航、文章列表、正文和辅助信息的字号与字重。界面控件与正文排版独立，保留自选字体、字号和行距。
- **三栏层次**：以统一的暖灰色系区分订阅栏、文章列表和阅读区，优化选中底色与分隔线。
- **一体式顶栏**：去掉横贯窗口顶部的白色空带，将列表标题和阅读工具栏上移，保留 macOS 系统窗口按钮。
- **设置页布局**：标题与“返回阅读”移至顶栏，适配窄窗口和全屏布局，修正底部保存按钮的显示范围。
- **翻译分段切换**：「原文／译文／双语对照」采用滑动分段控件，支持方向键、Home 和 End。
- **摘要展开**：摘要支持平滑展开、收起与箭头转动。当前文章已完成的摘要可直接重新展开；生成中收起会取消请求。
- **操作反馈**：收藏后显示实心星标与选中底色；摘要和划词译文复制后短暂显示成功勾选；工具栏按钮增加轻微按压与颜色过渡。
- **减少动态效果**：跟随系统设置简化切换和展开动画，保留清楚的操作状态。

## Changes since 1.1.1

- **Typography**: clearer sizes and weights for navigation, article lists, reading content and metadata, with interface controls independent of reading font preferences.
- **Three-pane surfaces**: distinct warm neutral backgrounds, refined selection colors and quieter separators.
- **Integrated titlebar**: removed the full-width blank strip, moved list headings and reading controls to the top, and preserved native macOS window buttons.
- **Settings layout**: moved the content heading and return action into the titlebar, improved narrow-window and full-screen layouts, and kept save controls visible.
- **Translation switching**: a sliding segmented control for original, translated and bilingual views, with arrow-key, Home and End navigation.
- **Summary expansion**: smooth expansion, collapse and chevron motion. Completed summaries reopen directly within the current article; collapsing an active summary cancels generation.
- **Action feedback**: filled stars and selection backgrounds for saved articles, temporary copy-success checks for summaries and selection translations, and subtle toolbar press and color transitions.
- **Reduce Motion**: simplified switching and expansion animations while retaining clear action states.
