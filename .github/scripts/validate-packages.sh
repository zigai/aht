#!/usr/bin/env bash
set -euo pipefail

ARCH=${TARGET#linux-}
DEB=$(jq -er --arg arch "$ARCH" '.[] | select(.type == "Linux Package" and .goarch == $arch and .extra.Format == "deb") | .name' dist/artifacts.json)
RPM=$(jq -er --arg arch "$ARCH" '.[] | select(.type == "Linux Package" and .goarch == $arch and .extra.Format == "rpm") | .name' dist/artifacts.json)
VERSION=$(jq -er .version dist/metadata.json)
COMMIT=$(jq -er .commit dist/metadata.json)

sudo dpkg -i "dist/$DEB"
/usr/bin/aht --json --version | jq -e --arg version "$VERSION" --arg commit "$COMMIT" '.commit as $built | .version == $version and ($built != "" and ($commit | startswith($built)))'

docker run --rm --mount "type=bind,src=$PWD/dist,dst=/packages,readonly" fedora:44 bash -euc 'dnf install --assumeyes "/packages/$1" >&2; /usr/bin/aht --json --version' -- "$RPM" | jq -e --arg version "$VERSION" --arg commit "$COMMIT" '.commit as $built | .version == $version and ($built != "" and ($commit | startswith($built)))'
