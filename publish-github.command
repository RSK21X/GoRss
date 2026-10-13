#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
repo='RSK21X/GoRss'
tag='v1.0.2'
assets="${GORSS_ASSET_DIR:-$PWD/../build/release-1.0.2}"
command -v gh >/dev/null || { echo '请安装 GitHub CLI：brew install gh'; exit 1; }
command -v git >/dev/null || exit 1
gh auth status >/dev/null 2>&1 || { echo '请先运行 gh auth login，然后重新执行此脚本。'; exit 1; }
[ -d .git ] || { echo '请从准备好的真实 GoRss Git 仓库运行。'; exit 1; }
[ "$(git branch --show-current)" = main ] || exit 1
[ -z "$(git status --porcelain)" ] || { echo '仓库有未提交改动，请先检查。'; exit 1; }
case "$(git remote get-url origin)" in
 https://github.com/RSK21X/GoRss.git|https://github.com/RSK21X/GoRss|git@github.com:RSK21X/GoRss.git) ;;
 *) echo 'origin 与授权仓库不符。'; exit 1;;
esac
[ "$(gh api repos/RSK21X/GoRss --jq '.full_name + ":" + (.private|tostring) + ":" + (.permissions.push|tostring)')" = 'RSK21X/GoRss:false:true' ] || { echo '目标必须是授权的公开仓库且当前账号拥有 push 权限。'; exit 1; }
expected=$(cat "$assets/RELEASE_COMMIT.txt")
[ "$(git rev-parse HEAD)" = "$expected" ] || { echo '当前提交与已验证产物不一致。'; exit 1; }
for name in GoRss-1.0.2-arm64.zip SHA256SUMS.txt; do [ -s "$assets/$name" ] || exit 1; done
(cd "$assets" && shasum -a 256 -c SHA256SUMS.txt)
git fetch origin main --tags
git merge-base --is-ancestor origin/main HEAD || { echo '远端已变化，需要合并并重新验证；脚本不会强推。'; exit 1; }
if git show-ref --verify --quiet "refs/tags/$tag"; then
 [ "$(git rev-parse "$tag^{commit}")" = "$expected" ] || { echo '已有 tag 指向不同提交，停止。'; exit 1; }
fi
if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
 echo 'Release 已存在，未覆盖。'
 gh release view "$tag" --repo "$repo" --json url,assets
 exit 0
fi
gh auth setup-git
git push origin HEAD:main
if ! git show-ref --verify --quiet "refs/tags/$tag"; then git tag "$tag" "$expected"; fi
git push origin "refs/tags/$tag"
gh release create "$tag" "$assets/GoRss-1.0.2-arm64.zip" "$assets/SHA256SUMS.txt" \
 --repo "$repo" --verify-tag --target "$expected" --title 'GoRss 1.0.2' --notes-file release-v1.0.2.md
gh release view "$tag" --repo "$repo" --json url,assets
