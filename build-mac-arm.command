#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
if [ "$(uname -s)" != "Darwin" ]; then
  echo "请在 macOS 上运行，以生成并签名 .app 和 .dmg。"
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  echo "请先安装 Go 1.27.1 或更新版本：https://go.dev/dl/"
  exit 1
fi
export GOTOOLCHAIN=auto
export CGO_ENABLED=0
echo "格式化并测试 RSS 后端…"
go fmt ./...
go mod tidy
go test ./...
go vet ./...
echo "构建 Apple Silicon 应用…"
go run github.com/egoist/mygo/cmd/mygo@v0.4.5 build -platform darwin/arm64
echo "构建完成：build/darwin-arm64"
open "build/darwin-arm64"
