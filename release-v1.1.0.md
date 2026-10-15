# GoRss 1.1.0

本地构建日期：2026-10-15。适用于 Apple Silicon，macOS 12 或更新版本。安装包尚未发布到远程仓库。

## AI 阅读

- 摘要、划词翻译和全文翻译支持流式显示。收到内容后逐步显示，不必等待完整请求结束。各供应商可单独关闭流式显示。
- 生成过程中可点击「停止生成」。全文翻译停止后保留已验证批次，重试可复用这些批次。未完成的临时预览不会写入缓存。
- 可添加最多 64 个额外的 OpenAI 兼容供应商。各供应商独立保存地址、模型、语言、超时、流式开关、提示词和 Keychain 凭据。已有自定义 API 与 Magpie 配置继续保留。
- 开放自定义摘要和翻译提示词。支持 `{language}`，摘要还支持 `{kind}`。留空使用默认提示词，也可填入默认值再编辑。实际提示词参与缓存标识，修改后不会误用旧结果。
- 双语对照提供浅色底、侧线、纯文本三种译文样式。译文保留阅读字号、行距与 Markdown 结构；相同代码块只显示一次。

当前实现 OpenAI 兼容 Chat Completions 协议。Anthropic 与 Gemini 原生协议未包含在本版本中。服务明确拒绝 `stream` 参数时允许一次非流式回退，并在当前 Service 中记住结果，避免后续翻译批次重复发送不受支持的参数。服务直接返回完整 JSON 时也可读取。

全文翻译的流式内容属于临时预览。程序只在完整批次通过 JSON、段落 ID、内容和完成原因校验后保存结果。意外断流、取消、截断及错误响应不会把部分内容写入成功缓存。原始文章、已读状态和收藏状态不会被 AI 输出覆盖。

## 设置交互

- 将 AI 设置分为「连接与模型」和「提示词」。供应商切换保留各自草稿，模型列表支持按名称或 ID 搜索。
- 固定底栏显示未保存状态，并提供「保存 AI 设置」和「放弃更改」。设置搜索可直接找到提示词编辑器。
- 供应商名称可以修改。新增供应商使用稳定标识，改名不会更换密钥账户。
- 可移除新增供应商配置。移除确认框支持 Escape 返回设置。内置自定义 API 与 Magpie 配置保留，可重命名或停用。

凭据保存和删除是独立的即时操作，「放弃更改」不会撤销凭据操作。移除供应商配置也不会删除 Keychain 凭据；如需清理，请先点击该供应商的「删除凭据」。

## 保留的订阅改进

包含 1.0.3 的 MyGo v0.5.1 升级、启动后台刷新、最多 4 个订阅并发刷新，以及 ETag／Last-Modified 条件请求。订阅刷新合并最新已读与收藏状态。HTTP 304 复用缓存，状态和校验信息未变时不重写数据文件。

## 验证

环境：Apple M4，darwin/arm64，Go 1.27.2，MyGo v0.5.1。

普通测试、竞态检测、`CGO_ENABLED=0` 静态检查、关闭调试检查器的发布模式测试、依赖校验及差异格式检查均通过。

新增测试覆盖：首段显示时机、正常完成缓存、断流、取消、无效事件、截断、Unicode 与未完成转义、明确拒绝流式请求的回退、后续批次避免重复回退、返回完整 JSON 的兼容服务、配置变化后的旧响应拦截、翻译临时预览与最终校验、供应商隔离、旧配置迁移、提示词缓存失效、草稿放弃、模型搜索、移除确认和译文样式。

真实 Mac 窗口检查使用临时数据、本地 HTTP 服务和隔离密钥存储。确认摘要与全文译文在请求结束前显示；确认双语渲染、供应商切换、提示词保存、取消划词请求，以及已读和收藏状态保留。本次没有调用真实收费模型，也没有修改用户的真实订阅、配置或 Keychain。

已检查宽屏与窄屏设置界面、提示词编辑器和三种双语样式。截图与检查记录位于 `build/release-1.1.0/checks/`。

App 原件、ZIP 解压件和 DMG 挂载件均通过严格签名检查。ZIP CRC、所有 App 文件的 SHA-256、ARM64 架构、版本、执行权限和许可文件一致性均通过。DMG 校验与只读挂载通过，安装卷含 Applications 快捷方式。

## 安装包

| 文件 | 大小 |
| --- | ---: |
| `build/release-1.1.0/GoRss-1.1.0-arm64.dmg` | 5,080,353 字节 |
| `build/release-1.1.0/GoRss-1.1.0-arm64.zip` | 6,644,237 字节 |
| `.app` 内全部文件 | 17,743,577 字节 |

校验值见同目录 `SHA256SUMS.txt`。App 位于 `build/release-1.1.0/darwin-arm64/GoRss.app`。

安装前退出旧版。打开 DMG，将 GoRss 拖入 Applications；或解压 ZIP 后移动 App。订阅、文章和设置沿用原数据目录。构建使用 ad hoc 签名，未使用 Developer ID 签名或公证。

使用方法见 [AI 阅读说明](AI_READING.md)。

## English

GoRss 1.1.0 adds streaming summaries, selection translations and full-document previews, with cancellation and validated translation batch reuse. Add up to 64 extra OpenAI-compatible providers with separate models, credentials and editable prompts. Existing Custom API and Magpie profiles remain available. AI settings now include connection and prompt tabs, searchable models and a fixed save/discard footer. Bilingual translations offer soft-background, side-rule and plain styles.

Full tests, race detection, static checks, production-mode tests and dependency verification passed. A native Mac window check verified progressive rendering, cancellation, provider and prompt persistence, and retained read/starred state using isolated local services. No paid external model or user Keychain was used. ZIP and DMG contents, signatures, permissions, versions and checksums were verified.

Apple Silicon, macOS 12+. Quit the previous version before replacing the App. Existing user data stays in the same directory. Packages use ad hoc signing and are not Developer ID signed or notarized. This is a local build.
