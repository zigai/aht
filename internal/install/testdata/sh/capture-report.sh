#!/bin/sh
{
  printf '%s\n' '---'
  printf '%s\n' "$@"
} >> "${AHT_CAPTURE:?}"
