# GoRss

[简体中文](README.zh-CN.md)

GoRss is a native RSS and Atom reader for Apple Silicon Macs, built with [MyGo](https://mygo.egoist.dev/) and [Onevra](https://github.com/oneo-me/onevra). It combines subscription navigation, an article list, and a quiet reading pane in a light interface.
<img width="2760" height="1716" alt="image" src="https://github.com/user-attachments/assets/02a39df8-0fc0-4abf-bcd1-776c5feb0625" />

## Features

Add, remove, and refresh RSS/Atom subscriptions; search article titles and summaries; filter unread and starred articles; and import or export subscriptions as OPML. Articles support Markdown, images, tables, quotes, and highlighted code blocks. Feed HTML is converted to Markdown; complex web layouts and interactive content are not reproduced.

Reading settings include a searchable dropdown of locally installed macOS fonts, font size, line spacing, content width, and image visibility. Settings and reading state persist locally. Articles provided by feeds are cached; remote images require a connection. The reader does not fetch full web pages when a feed provides only a summary.

## Install

Download the Apple Silicon DMG or ZIP from this repository's Releases page. Open the DMG, then drag GoRss into Applications. Quit any previous version before replacing the app. Existing subscriptions and settings are retained.

Builds require macOS 12 or later. Packages are not Developer ID signed or notarized; macOS may require explicit approval in System Settings → Privacy & Security.

## Build

Install Go 1.27.1 or later, clone the repository, and run on macOS:

```bash
go mod tidy
go test ./...
go vet ./...
go run github.com/egoist/mygo/cmd/mygo@v0.4.5 build -platform darwin/arm64
```

Output is written to `build/darwin-arm64/`. The build script is another option: `bash build-mac-arm.command`. No Node or frontend build step is required. MyGo is pinned to v0.4.5 and Onevra to v0.2026.6.

## Settings and data

Use the lower-left Reading settings button or ⌘, to change typography. Font changes are reflected in the preview; Save settings applies and persists them. Newly installed fonts appear after restarting the app. The interface uses a light theme.

Data is stored under `~/Library/Application Support/GoRss/`: `reader.json` contains subscriptions, articles, and read/starred state; `preferences.json` contains typography settings. OPML exports subscriptions only. For a complete backup, quit the app and copy both JSON files.

⌘N adds a subscription, ⌘R refreshes, ⌘, opens settings, ⌘W closes the window, and ⌘Q quits.

## Validation and limits

Version 1.0.1 has passed backend and native headless UI tests, race detection, static analysis, and Darwin ARM64 cross-compilation. Release archives were checked for filesystem integrity, checksums, payload consistency, and permissions. Actual macOS window launch, CoreText enumeration, native dialogs, and DMG mounting were not exercised in the build environment. Headless performance benchmarks are not measurements of Mac display FPS.

Recent fixes preserve scroll position across settings, avoid image reloads when only typography changes, preserve settings edits during startup, improve HTML/Markdown conversion, and protect read/starred state across feed updates. Very long documents still incur whole-document layout costs to retain cross-paragraph text selection.

## Dependencies and attribution

MyGo and Onevra supply native windows, controls, themes, and Markdown rendering. RSS parsing uses gofeed. See `THIRD_PARTY_NOTICES.md` for dependency attribution. The application icon is provided by the project owner. This repository does not grant a new license for the application source or artwork; dependency licenses remain applicable.
