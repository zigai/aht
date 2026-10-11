#!/usr/bin/env bash
set -euo pipefail

PREVIOUS_TAG=$(gh api "repos/$GITHUB_REPOSITORY/releases" --paginate --slurp | jq -er --arg current "${GITHUB_REF_NAME:-}" '[.[][] | select(.draft == false and .prerelease == false and .tag_name != $current)][0].tag_name // error("no previous published release")')
PREVIOUS_DIR="$RUNNER_TEMP/aht-previous-$TARGET"
mkdir -p "$PREVIOUS_DIR"
gh release download "$PREVIOUS_TAG" --repo "$GITHUB_REPOSITORY" --pattern "aht_*_${TARGET/-/_}.tar.gz" --output "$PREVIOUS_DIR/release.tar.gz"
tar -xzf "$PREVIOUS_DIR/release.tar.gz" -C "$PREVIOUS_DIR" aht
chmod 0755 "$PREVIOUS_DIR/aht"
echo "Previous published release: $PREVIOUS_TAG ($TARGET)"
"$PREVIOUS_DIR/aht" --version
echo "AHT_PREVIOUS_BINARY=$PREVIOUS_DIR/aht" >> "$GITHUB_ENV"
