# GoRss

[简体中文](README.zh-CN.md)

GoRss is a native RSS and Atom reader for Apple Silicon Macs, built with [MyGo](https://mygo.egoist.dev/) and [Onevra](https://github.com/oneo-me/onevra). It combines subscription navigation, an article list, and a quiet reading pane in a light interface.

![GoRss 1.2.1 reader and AI summary](docs/screenshots/reader.png)

## Features

Cached articles appear at launch while all subscriptions refresh in the background. A failed refresh keeps cached articles and reports the error. Up to four subscriptions refresh concurrently. ETag and Last-Modified conditional requests reuse cached articles on HTTP 304. Add, remove, and refresh RSS/Atom subscriptions; search article titles and summaries; filter unread and starred articles; and import or export subscriptions as OPML. Articles support Markdown, images, tables, quotes, and highlighted code blocks. Feed HTML is converted to Markdown; complex web layouts and interactive content are not reproduced.

Reading settings include a searchable dropdown of locally installed macOS fonts, font size, line spacing, content width, and image visibility. Interface typography stays separate from reading preferences. Three warm surfaces distinguish subscriptions, article lists and reading content. An integrated macOS titlebar brings the article list and reading controls to the top while preserving native window buttons. Settings adapt to narrow windows and full screen. Settings and reading state persist locally. Articles provided by feeds are cached; remote images require a connection. The reader does not fetch full web pages when a feed provides only a summary.

## AI reading

AI is disabled by default. Article toolbar actions generate summaries and translate articles on demand, with original, translated, and bilingual views. Select text and use the context menu to translate up to 4,000 characters. Long articles are translated in Markdown-aware batches, displayed as each batch completes. Retry reuses successful batches; regeneration bypasses cached results. Code, images, and link destinations are preserved, and RSS content is never overwritten.

Add multiple OpenAI-compatible providers, each with its own name, endpoint, model, credential account, and summary/translation prompts. Settings include model search, prompt editors, a fixed save bar, and draft discard. Summaries expand and collapse smoothly, and translation modes use a sliding segmented control. Star and copy actions provide visual feedback; motion follows the system Reduce Motion preference. Reading settings offer a soft background, a side rule, or plain text for bilingual translations, with a live preview. Existing custom API and Magpie profiles remain available.

Open Settings from the sidebar or ⌘,. Reading settings control typography, images, and bilingual translation styles; the AI category saves its configuration separately. Custom providers use OpenAI-compatible `/models` and `/chat/completions` endpoints: enter a base URL, actual model ID, target language, and timeout. Magpie defaults to `http://127.0.0.1:3425/v1`; detection checks `/api/hello` identity before `/v1/models`. Model testing sends a real request. Save/delete credentials explicitly; an empty field keeps the existing key. See [AI reading details](AI_READING.md).

## Screenshots

<details>
<summary>Bilingual reading</summary>

![Original and translated paragraphs with the translation mode control](docs/screenshots/bilingual.png)

</details>

<details>
<summary>Reading settings and bilingual style preview</summary>

![Reading settings with a live bilingual style preview](docs/screenshots/settings.png)

</details>

## Install

Download the Apple Silicon DMG or ZIP from the [latest release](https://github.com/RSK21X/GoRss/releases/latest). Open the DMG, then drag GoRss into Applications. Quit any previous version before replacing the app. Existing subscriptions and settings are retained.

Builds require macOS 12 or later. Packages are not Developer ID signed or notarized; macOS may require explicit approval in System Settings → Privacy & Security.

## Build

Install Go 1.27.1 or later, clone the repository, and run on macOS:

```bash
go mod tidy
go test ./...
go vet ./...
go run github.com/egoist/mygo/cmd/mygo@v0.5.1 build -platform darwin/arm64
```

Output is written to `build/darwin-arm64/`. The build script is another option: `bash build-mac-arm.command`. No Node or frontend build step is required. MyGo is pinned to v0.5.1 and Onevra to v0.2026.6.

The app and build CLI both use upstream MyGo v0.5.1. Selection translation uses the public `Element.SelectedTexts()` API. No local MyGo fork or `replace` directive is needed.

## Settings and data

Use the Reading category in the lower-left Settings page or ⌘, to change typography. Font changes are reflected in the preview; Save settings applies and persists them. Newly installed fonts appear after restarting the app. The interface uses a light theme.

Data is stored under `~/Library/Application Support/GoRss/`: `reader.json` contains subscriptions, articles, and read/starred state; `preferences.json` contains typography settings. OPML exports subscriptions only. For a complete backup, quit the app and copy the data directory. `ai.json` stores AI configuration without keys. Credentials are stored in macOS Keychain; `ai-cache/` holds bounded plaintext derived results and can be deleted. Only explicit AI actions send available article content or selected text to the chosen provider. Remote services require HTTPS; HTTP is limited to loopback. Cached summaries and translations may contain private article information.

⌘N adds a subscription, ⌘R refreshes, ⌘, opens settings, ⌘W closes the window, and ⌘Q quits.

## Validation and limits

See the [1.2.1 release notes](release-v1.2.1.md) for feature changes, and the [MyGo review](MYGO_0.5_REVIEW.md) for framework measurements and follow-up work. Native macOS window startup and CoreText font enumeration were checked. Native file dialogs, the real Keychain, and live AI services were not exercised in this round. Headless performance tests do not measure macOS display FPS.

AI displays SSE output progressively. Summaries and selection translations show arriving text. Article translations show provisional previews and cache only fully validated batches. Generation can be stopped and retried. Disable streaming for incompatible services; an explicit rejection of the stream parameter permits one fallback. Latency depends on the model, network, and article length. Bilingual selection is limited across independent paired blocks; alignment failures are explained with a translated-view fallback. Very long articles still incur whole-document layout costs. AI results can be inaccurate, and summaries only use content available from the feed.

## Dependencies and attribution

MyGo and Onevra supply native windows, controls, themes, and Markdown rendering. RSS parsing uses gofeed. See `THIRD_PARTY_NOTICES.md` for dependency attribution. The application icon is provided by the project owner. This repository does not grant a new license for the application source or artwork; dependency licenses remain applicable.
