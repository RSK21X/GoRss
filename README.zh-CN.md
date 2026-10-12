# GoRss

[English](README.md)

GoRss 是基于 [MyGo](https://mygo.egoist.dev/) 与 [Onevra](https://github.com/oneo-me/onevra) 制作的 Apple Silicon Mac 原生 RSS/Atom 阅读器，采用浅色三栏界面。
<img width="2760" height="1716" alt="image" src="https://github.com/user-attachments/assets/9d09bc39-384e-47f5-a92d-0557565748b3" />

## 功能

支持添加、删除和刷新 RSS/Atom 订阅，搜索文章标题及摘要，筛选未读与收藏文章，以及 OPML 导入导出。正文支持 Markdown、图片、表格、引用和代码高亮。RSS 的 HTML 转换为 Markdown 显示，不复现复杂网页布局或交互。

阅读设置提供可搜索的 Mac 已安装字体下拉列表、字号、行距、正文宽度和图片开关。设置与阅读状态保存在本地。订阅提供的正文可以缓存阅读，外部图片需要联网；订阅只提供摘要时不会额外抓取网页全文。

## 安装

在本仓库 Releases 页面下载 Apple Silicon DMG 或 ZIP。打开 DMG，将 GoRss 拖入“应用程序”。更新前先退出旧版再替换，已有订阅与设置会保留。

构建要求 macOS 12 或更新版本。安装包未使用 Developer ID 签名或公证；初次打开可能需要在系统设置“隐私与安全性”中明确允许。

## 构建

安装 Go 1.27.1 或更新版本，克隆仓库后在 Mac 上运行：

```bash
go mod tidy
go test ./...
go vet ./...
go run github.com/egoist/mygo/cmd/mygo@v0.4.5 build -platform darwin/arm64
```

产物位于 `build/darwin-arm64/`。也可以执行 `bash build-mac-arm.command`。无需 Node 或网页构建步骤。MyGo 固定为 v0.4.5，Onevra 固定为 v0.2026.6。

## 设置与数据

点击左下角“阅读设置”或按 ⌘, 调整排版。字体选择立即更新预览，点击“保存设置”后应用到正文并持久保存。新安装字体需重启应用后显示。应用固定为浅色主题。

数据位于 `~/Library/Application Support/GoRss/`：`reader.json` 保存订阅、文章及已读收藏状态；`preferences.json` 保存排版设置。OPML 仅备份订阅。完整备份请在退出应用后复制两个 JSON 文件。

快捷键为 ⌘N 添加订阅、⌘R 刷新、⌘, 打开设置、⌘W 关闭窗口、⌘Q 退出。

## 验证与限制

1.0.1 已通过后端与原生无窗口界面测试、并发检测、静态检查和 Darwin ARM64 交叉编译。安装包已校验文件系统、校验码、包内文件一致性与权限。构建环境未执行实际 Mac 窗口启动、CoreText 字体枚举、原生对话框和 DMG 挂载。无窗口性能测试不代表 Mac 实际显示帧率。

本版本修复设置切换时阅读位置重置、仅改排版也重复加载图片、启动设置覆盖编辑内容、HTML/Markdown 转换异常和订阅更新时阅读状态丢失等问题。超长正文仍存在整体排版开销，以保留跨段落文字选择能力。

## 依赖与归属

MyGo 与 Onevra 提供原生窗口、组件、主题和 Markdown 渲染，RSS 解析使用 gofeed。依赖归属见 `THIRD_PARTY_NOTICES.md`。应用图标由项目所有者提供。本仓库没有为应用源码或图标新增授权许可，依赖许可仍然适用。
