# GoRss 1.0.3

本地构建日期：2026-10-15。适用于 Apple Silicon，macOS 12 或更新版本。安装包尚未发布到远程仓库。

## 改动

- 升级至 MyGo v0.5.1，改用公开的选区与字体接口，移除本地框架副本。修复字体和模型下拉框的选择兼容性问题。
- 修复启动刷新：先显示缓存，再后台刷新全部订阅。
- 每批最多 4 个订阅并发刷新。请求完成后，空闲任务立即处理下一个订阅。各订阅的失败独立报告，成功结果仍会保存。
- 持久保存 ETag、Last-Modified 与最终响应地址，重启后继续发送条件请求。校验信息只用于匹配的地址，包含路径和查询参数。
- HTTP 304 复用缓存，不解析正文。校验信息和错误状态未变时，不重写数据文件。304 若更新校验信息或清除旧错误，仍会原子保存。
- 保留已读、收藏及原有文章标识。刷新写入时合并最新状态，包含网络请求期间对已读、收藏的修改。
- 网络错误、解析失败、取消和保存失败不会用新校验信息覆盖旧缓存。200 未提供校验信息时清除旧值；304 未提供的值继续保留。
- 将已有第三方许可说明随 App 一起打包。

`LastFetched` 记录最近一次完整获取正文的时间，304 不修改该字段。每次启动刷新一次；没有加入周期刷新或断网恢复后的自动重试。

## 验证

环境：Apple M4，darwin/arm64，Go 1.27.2。普通测试、数据竞争检查、`CGO_ENABLED=0` 静态检查与发布模式测试均通过。并发与条件请求测试另在数据竞争检查下重复 10 轮，通过。

新增测试覆盖：并发上限与队列推进、200／304 混合结果、刷新期间已读／收藏的双向修改、取消后停止未开始的请求、失败隔离、错误排序、条件信息重启复用、重定向路径／查询／主机变化、无效正文、空订阅源、304 无写入，以及保存失败后的恢复。

真实 Mac 窗口检查使用临时数据和本地 HTTP 服务：8 个订阅达到 4 个并发请求，启动时缓存可见，混合 200／304 更新后已读、收藏保留。第二轮全部返回 304 时，确认没有重写数据文件。没有刷新用户的真实订阅。

App 原件、ZIP 解压件和 DMG 挂载件均通过严格签名检查。已校验 ZIP CRC、包内所有文件的 SHA-256 一致性、版本、平台、权限及许可文件。DMG 校验与只读挂载通过，安装卷含 Applications 快捷方式。日志位于 `build/release-1.0.3/checks/`。

## 安装包

| 文件 | 大小 |
| --- | ---: |
| `build/release-1.0.3/GoRss-1.0.3-arm64.dmg` | 5,047,968 字节 |
| `build/release-1.0.3/GoRss-1.0.3-arm64.zip` | 6,612,471 字节 |
| `.app` 内全部文件 | 17,660,393 字节 |

校验值见同目录 `SHA256SUMS.txt`。App 位于 `build/release-1.0.3/darwin-arm64/GoRss.app`。

安装前退出旧版。打开 DMG，将 GoRss 拖入 Applications；或解压 ZIP 后移动 App。已有订阅、文章和设置沿用原数据目录。构建使用 ad hoc 签名，未使用 Developer ID 签名或公证。

本次没有操作真实 Keychain、真实 AI 服务或原生文件选择对话框。AI、图片与全文获取的既有限制继续适用。

## English

GoRss 1.0.3 upgrades to MyGo v0.5.1, fixes startup refresh, and refreshes up to four feeds concurrently. It persists ETag and Last-Modified validators scoped to the final response URL. HTTP 304 reuses cached articles and avoids rewriting the store when no metadata or error state changes. Read and starred flags are merged from the latest state and retained across refreshes.

Full tests, race detection, static checks, production-mode tests, and ten rounds of refresh regression tests passed on macOS. A native-window check with eight local feeds verified the concurrency ceiling, mixed 200/304 results, cached reading during startup, retained flags, and a second all-304 refresh without a store rewrite. ZIP and DMG contents, signatures, version, permissions, and checksums were verified.

Apple Silicon, macOS 12+. Quit the previous version before replacing the App. Existing data remains in the same user configuration directory. Packages use ad hoc signing and are not Developer ID signed or notarized. This is a local build; no remote release was published.
