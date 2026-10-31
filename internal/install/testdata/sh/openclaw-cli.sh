#!/bin/sh
set -eu
state=${OPENCLAW_TEST_STATE:?}
printf '%s\n' "$*" >> "$state/calls"
if [ "$1 $2" = "plugins inspect" ]; then
  if [ -f "$state/inspect-json" ]; then
    cat "$state/inspect-json"
    exit 0
  fi
  if [ ! -f "$state/source" ]; then
    printf '[]\n'
    exit 0
  fi
  source=$(cat "$state/source")
  policy=false
  if [ -f "$state/policy" ]; then policy=true; fi
  version="0.0.9"
  if [ -f "$source/package.json" ]; then
    version=$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' "$source/package.json" | head -n1)
  fi
  printf '[{"plugin":{"id":"aht-state","status":"loaded","source":"path","version":"%s"},"policy":{"allowConversationAccess":%s},"install":{"source":"path","sourcePath":"%s","installPath":"%s","version":"%s"}}]\n' "$version" "$policy" "$source" "$source" "$version"
  exit 0
fi
if [ "$1 $2" = "plugins install" ]; then
  printf '%s' "$3" > "$state/source"
  exit 0
fi
if [ "$1 $2" = "plugins uninstall" ]; then
  rm -f "$state/source" "$state/policy"
  exit 0
fi
if [ "$1 $2" = "config set" ]; then
  : > "$state/policy"
  exit 0
fi
printf 'unexpected fake OpenClaw command: %s\n' "$*" >&2
exit 2
