#!/bin/sh
# Only the first report stalls; the rest record themselves without a Node start,
# which keeps the queued reports inside the driver deadline on slow runners.
capture=${AHT_PROCESS_CAPTURE:?}
if [ ! -e "$capture" ]; then
    exec "${AHT_STALLED_REPORTER:?}" "$@"
fi
overlap=false
previous=$(sed -n '$s/^{"pid":\([0-9]*\),.*/\1/p' "$capture")
if [ -n "$previous" ] && kill -0 "$previous" 2>/dev/null; then
    overlap=true
fi
awk -v pid="$$" -v overlap="$overlap" '
function escape(value) {
  gsub(/\\/, "\\\\", value)
  gsub(/"/, "\\\"", value)
  gsub(/\n/, "\\n", value)
  gsub(/\r/, "\\r", value)
  gsub(/\t/, "\\t", value)
  return value
}
BEGIN {
  line = "{\"pid\":" pid ",\"args\":["
  for (i = 1; i < ARGC; i++) line = line (i > 1 ? "," : "") "\"" escape(ARGV[i]) "\""
  print line "],\"overlap\":" overlap "}" >> ENVIRON["AHT_PROCESS_CAPTURE"]
  exit
}' "$@"
