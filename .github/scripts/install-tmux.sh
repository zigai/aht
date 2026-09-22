#!/usr/bin/env bash
set -euo pipefail

: "${AHT_TMUX_PREFIX:?Set a local installation prefix}"
if ! { pkg-config --exists libevent && { pkg-config --exists ncurses || pkg-config --exists ncursesw; }; }; then
  sudo apt-get update
  sudo apt-get install --yes libevent-dev ncurses-dev build-essential bison pkg-config
fi

aht_tmux_work=$(mktemp -d)
trap 'rm -rf "$aht_tmux_work"' EXIT
cd "$aht_tmux_work"
curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 \
  -o tmux-3.6.tar.gz https://github.com/tmux/tmux/releases/download/3.6/tmux-3.6.tar.gz
# Published GitHub release asset digest for tmux 3.6.
echo '136db80cfbfba617a103401f52874e7c64927986b65b1b700350b6058ad69607  tmux-3.6.tar.gz' | sha256sum --check --strict
tar -xzf tmux-3.6.tar.gz
cd tmux-3.6
./configure --prefix="$AHT_TMUX_PREFIX"
make -j"$(nproc)"
make install
test "$("$AHT_TMUX_PREFIX/bin/tmux" -V)" = 'tmux 3.6'
