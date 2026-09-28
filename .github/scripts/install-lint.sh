#!/usr/bin/env bash
# Install the golangci-lint release pinned in the Justfile into $AHT_LINT_DIRECTORY,
# verifying the archive against .github/golangci-lint-checksums.txt.
set -euo pipefail

readonly INSTALL_DIR="${AHT_LINT_DIRECTORY:?Set a local installation directory}"
readonly CHECKSUMS_FILE="$PWD/.github/golangci-lint-checksums.txt"
VERSION=$(just --evaluate golangci_lint_version)
readonly VERSION
readonly NAME="golangci-lint-${VERSION#v}-$(go env GOOS)-$(go env GOARCH)"
readonly ARCHIVE="$NAME.tar.gz"
readonly URL="https://github.com/golangci/golangci-lint/releases/download/$VERSION/$ARCHIVE"

expected_checksum() {
    awk -v archive="$ARCHIVE" '$2 == archive { print $1 }' "$CHECKSUMS_FILE"
}

checksum=$(expected_checksum)
if [[ ${#checksum} -ne 64 ]]; then
    echo "error: no SHA-256 checksum for $ARCHIVE in $CHECKSUMS_FILE" >&2
    exit 1
fi

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
cd "$work_dir"

curl --fail --silent --show-error --location \
    --retry 5 --retry-all-errors --retry-delay 2 \
    --output "$ARCHIVE" \
    "$URL"
echo "$checksum  $ARCHIVE" | shasum --algorithm 256 --check

tar -xzf "$ARCHIVE"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$NAME/golangci-lint" "$INSTALL_DIR/golangci-lint"
