# GoRss 1.0.2

## 简体中文

- 更新三栏阅读界面、文章操作菜单和统一设置页；阅读排版与 AI 配置独立保存。
- 新增按需摘要、结构化全文翻译、原文／译文／双语切换及右键划词翻译。
- 支持自定义 OpenAI 兼容服务与 Magpie，包含真实协议检测和模型测试；API Key 保存在 macOS Keychain。
- 添加有界持久 AI 缓存、已完成批次复用、取消及过期响应防护。
- 优化翻译连接复用、输出预算和结构化响应兼容；修复 Markdown 正文排版。
- 更新双语文档、私密文件忽略规则及依赖许可说明。保留本地 MyGo 选区访问适配及完整 runtime 源码。

安装：Apple Silicon，macOS 12+。下载 ZIP，解压后把 GoRss.app 移到“应用程序”，更新前退出旧版。附 SHA256SUMS.txt。此次提供 ZIP；没有制作新版 DMG。安装包未使用 Developer ID 签名或公证。

验证：Linux 上全项目 `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...` 通过，Darwin ARM64 App 交叉构建成功；ZIP 内容、权限、版本与 SHA-256 已校验。实际 macOS GUI、CoreText、原生对话框、Keychain、真实 AI 服务及系统安装体验未验证。

限制：AI 当前使用 `stream: false`，完成一批后更新界面；双语跨独立结构块选区受限。远程图片需联网，订阅只提供摘要时不抓取网页全文。AI 可能有误，主动请求才发送正文或选区；本地明文缓存可能包含私人文章派生内容。

## English

- Updated the three-pane reader, article action menus, and unified Settings page, with separate reading and AI saves.
- Added on-demand summaries, structured article translation, original/translated/bilingual views, and selection translation from the context menu.
- Added custom OpenAI-compatible and Magpie providers, protocol detection, and real model tests; keys are stored in macOS Keychain.
- Added bounded persistent AI results, completed-batch reuse, cancellation, and stale-response protection.
- Improved translation connection reuse, output budgeting, and structured-response compatibility; fixed Markdown typography.
- Updated bilingual documentation, private-file ignores, and dependency notices. Included the complete local MyGo runtime and selection accessor adaptation.

Install: Apple Silicon, macOS 12+. Extract the ZIP and move GoRss.app to Applications, quitting the old version first. SHA256SUMS.txt is included. This release preparation provides a ZIP; no new DMG was produced. The package is not Developer ID signed or notarized.

Validation: full tests, race detection, and vet passed on Linux; the Darwin ARM64 App cross-build succeeded. ZIP contents, permissions, version, and SHA-256 were checked. Actual macOS GUI, CoreText, dialogs, Keychain, real AI services, and installation behavior were not exercised.

Limits: AI uses `stream: false` and updates after each complete batch. Selection across independent bilingual blocks is limited. Remote images need connectivity; summary-only feeds do not trigger full-page fetching. AI can be inaccurate. Only explicit requests send article or selection content; local plaintext results may contain private article information.
