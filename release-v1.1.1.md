# GoRss 1.1.1

构建日期：2026-10-15。适用于 Apple Silicon，macOS 12 或更新版本。

## 自 1.0.2 以来

- 升级至上游 MyGo v0.5.1，移除本地框架副本，改用公开的选区与字体接口。
- 修复启动刷新：先显示缓存，再后台刷新全部订阅。最多 4 个订阅并发刷新，支持 ETag／Last-Modified 条件请求，保留已读与收藏状态。
- AI 摘要、划词翻译和全文翻译支持流式显示与停止生成。全文翻译只缓存校验通过的批次，失败重试可复用成功结果。
- 支持添加多个 OpenAI 兼容供应商，独立管理模型、密钥、语言、超时和提示词。已有自定义 API 与 Magpie 配置继续保留。
- 开放自定义摘要、翻译提示词。优化供应商切换、模型搜索、提示词编辑与保存／放弃草稿交互。

## 改动

- 将「双语译文样式」从 AI 设置移到「阅读 → 双语阅读」。通过阅读设置底栏的「保存设置」保存。
- 新增原文与译文的双语预览。浅色底、侧线和纯文本样式均实时显示，字体、字号与行距也随阅读草稿更新。预览无需启用 AI，不发送网络请求。
- 预览与正文使用同一套译文样式和 Markdown 渲染代码。未保存的修改只影响预览；保存后应用到正文。
- 样式保存在 `GoRss/preferences.json`。阅读配置没有该字段时，自动沿用旧版 `ai.json` 中的有效样式。保存后以阅读配置为准，不改写旧 AI 配置或凭据。
- 设置搜索显示阅读结果时，同时显示阅读设置的保存栏。即使从 AI 栏开始搜索，也能直接保存样式。
- 「恢复默认」同时恢复默认译文样式。

保留 1.1.0 的流式 AI、多供应商、自定义提示词，以及启动刷新、最多 4 个并发订阅刷新、ETag／Last-Modified 条件请求。已有文章、已读与收藏状态继续沿用。

## 验证

普通测试、竞态检测、`CGO_ENABLED=0` 静态检查、关闭调试检查器的发布模式测试、依赖校验及差异格式检查均通过。

新增测试覆盖旧版样式迁移、无效旧配置回退、阅读配置优先级、无效样式拒绝、设置搜索中的保存操作、草稿与正文隔离、保存后持久化、字号和行距对预览的影响、三种样式的实际背景／侧线像素，以及 1380 和 420 像素宽度下的布局。

真实 Mac 窗口检查确认：流式生成与取消正常、双语正文正常、供应商与提示词可保存、阅读预览可渲染、阅读样式可独立保存，已读与收藏状态保留。检查使用临时数据、本地模拟 HTTP 服务和隔离凭据，没有修改真实用户数据或调用收费模型。

已检查完整阅读设置页面、宽屏／窄屏三种双语预览和大字号预览。截图与测试记录位于 `build/release-1.1.1/checks/`。

App 原件、ZIP 解压件、DMG 挂载件均通过严格签名检查。ZIP CRC、App 文件 SHA-256、架构、版本、执行权限和许可文件一致性均通过。DMG 校验与只读挂载通过，安装卷包含 Applications 快捷方式。

## 安装包

发布附件：`GoRss-1.1.1-arm64.dmg`、`GoRss-1.1.1-arm64.zip` 和 `SHA256SUMS.txt`。

退出旧版后替换 App，已有数据保留。安装包使用 ad hoc 签名，未使用 Developer ID 签名或公证。

## English

Since 1.0.2, GoRss upgrades to upstream MyGo v0.5.1, fixes startup refresh, adds up to four concurrent feed refreshes and ETag/Last-Modified conditional requests, and retains read/starred state. AI features now include streaming and cancellation, multiple OpenAI-compatible providers, editable prompts, searchable models and separate provider drafts.

GoRss 1.1.1 moves bilingual translation styles to Reading settings and adds a live bilingual preview. Preview and article rendering share the same style code. Draft font, size, line height and translation style update the preview immediately; saving applies the preferences to articles. Legacy styles migrate from AI configuration when no reading style has been saved.

Tests, race detection, static checks, production-mode tests, native Mac window checks and package verification passed. Apple Silicon, macOS 12+. Packages use ad hoc signing and are not notarized. Existing user data remains available.
