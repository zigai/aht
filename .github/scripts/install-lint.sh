#!/usr/bin/env bash
set -euo pipefail

: "${AHT_LINT_DIRECTORY:?Set a local installation directory}"
aht_lint_version=$(just --evaluate golangci_lint_version)
aht_lint_name="golangci-lint-${aht_lint_version#v}-$(go env GOOS)-$(go env GOARCH)"
aht_lint_checksum=$(awk -v name="$aht_lint_name.tar.gz" '$2 == name {print $1}' .github/golangci-lint-checksums.txt)
test "${#aht_lint_checksum}" = 64
aht_lint_work=$(mktemp -d)
trap 'rm -rf "$aht_lint_work"' EXIT
cd "$aht_lint_work"
curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 \
  -o "$aht_lint_name.tar.gz" "https://github.com/golangci/golangci-lint/releases/download/$aht_lint_version/$aht_lint_name.tar.gz"
echo "$aht_lint_checksum  $aht_lint_name.tar.gz" | shasum -a 256 --check
tar -xzf "$aht_lint_name.tar.gz"
mkdir -p "$AHT_LINT_DIRECTORY"
install -m 0755 "$aht_lint_name/golangci-lint" "$AHT_LINT_DIRECTORY/golangci-lint"
