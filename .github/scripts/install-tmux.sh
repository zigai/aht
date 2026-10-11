#!/usr/bin/env bash
# Build tmux from its verified release tarball into $AHT_TMUX_PREFIX.
set -euo pipefail

readonly PREFIX="${AHT_TMUX_PREFIX:?Set a local installation prefix}"
readonly VERSION="3.6"
readonly ARCHIVE="tmux-$VERSION.tar.gz"
readonly URL="https://github.com/tmux/tmux/releases/download/$VERSION/$ARCHIVE"
# Published GitHub release asset digest for tmux 3.6.
readonly CHECKSUM="136db80cfbfba617a103401f52874e7c64927986b65b1b700350b6058ad69607"

has_build_dependencies() {
    pkg-config --exists libevent &&
        { pkg-config --exists ncurses || pkg-config --exists ncursesw; }
}

if ! has_build_dependencies; then
    sudo apt-get update
    sudo apt-get install --yes libevent-dev ncurses-dev build-essential bison pkg-config
fi

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
cd "$work_dir"

curl --fail --silent --show-error --location \
    --retry 5 --retry-all-errors --retry-delay 2 \
    --output "$ARCHIVE" \
    "$URL"
echo "$CHECKSUM  $ARCHIVE" | sha256sum --check --strict

tar -xzf "$ARCHIVE"
cd "tmux-$VERSION"
./configure --prefix="$PREFIX"
make -j"$(nproc)"
make install

installed_version=$("$PREFIX/bin/tmux" -V)
if [[ $installed_version != "tmux $VERSION" ]]; then
    echo "error: expected tmux $VERSION, installed $installed_version" >&2
    exit 1
fi
