#!/bin/bash
# Prints the schema-1 request-filed event a stdin-reading on_request hook receives, built
# from one approval fixture: the fixture's own request view, re-timed so it is unexpired.
#
# usage: request-event.sh <fixture.json> [<seconds until expiry, default 900>]
# Exit 3, nothing on stdout and "skip <id>: <reason>" on stderr when the fixture is not a
# new pending request: a toast for it would show a request that was never filed that way.
set -eu
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || { echo "usage: $0 <fixture.json> [<seconds until expiry>]" >&2; exit 2; }
F=$1
TTL=${2:-900}
case $TTL in '' | *[!0-9]*) echo "seconds until expiry must be a whole number, got: $TTL" >&2; exit 2 ;; esac
id=$(jq -r .id "$F")
reason=$(jq -r '
  if .prompt != "request" or (.request | type) != "object" then "no request is filed (prompt: \(.prompt))"
  elif (.request.hook_failure // "") != "" then "its hook failed, so nothing was shown"
  elif .state != "pending" then "fixture state is \(.state), an outcome a new request toast does not render"
  elif .request.state != "pending" then "request state is \(.request.state), not pending"
  else "" end' "$F")
if [ -n "$reason" ]; then
  echo "skip $id: $reason" >&2
  exit 3
fi
jq -c --argjson ttl "$TTL" '(now | floor) as $n
  | {schema: 1, event: "request-filed", source: .request.source, time: ($n | todate),
     summary: (.hook_env.YOLO_REQUEST_SUMMARY // ""),
     request: (.request | .filed = ($n | todate) | .expires = (($n + $ttl) | todate) | del(.decided))}' "$F"
