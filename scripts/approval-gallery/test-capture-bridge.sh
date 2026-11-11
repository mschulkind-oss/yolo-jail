#!/bin/bash
# Tests the capture bridge without a display. First the stdin adapter a candidate capture uses: request-event.sh turns a PENDING
# fixture into the schema-1 request-filed event `yolo internal approval-notify` reads on
# stdin, and refuses every fixture that is not a new pending request view.
#
# usage: test-capture-bridge.sh [<candidate yolo binary>]
# Without a binary it builds ./cmd/yolo from this checkout. Needs jq. Uses no bus, no
# display and no approval store: the candidate is run with env -i and a logging-only stub.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
FIX=$ROOT/internal/approvalfixtures/fixtures
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
FAILS=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1" >&2; FAILS=$((FAILS + 1)); }
YOLO=${1:-}
if [ -z "$YOLO" ]; then
  YOLO=$T/yolo
  (cd "$ROOT" && go build -o "$YOLO" ./cmd/yolo) || { echo "cannot build ./cmd/yolo; pass a built candidate as \$1" >&2; exit 2; }
fi
YOLO=$(realpath "$YOLO")
printf '#!/bin/sh\necho "yolo $*" >>"%s"\n' "$T/stub.log" >"$T/stub-yolo"
chmod +x "$T/stub-yolo"
: >"$T/stub.log"

# 1. A pending fixture becomes one schema-1 request-filed event that has not expired.
ev=$T/event.json
if "$HERE/request-event.sh" "$FIX/git-push-force.json" >"$ev" 2>"$T/err" &&
  jq -e --slurpfile f "$FIX/git-push-force.json" '
    .schema == 1 and .event == "request-filed" and .request.schema == 1
    and .request.state == "pending" and (.request.expires | fromdate) > now
    and .request.id == $f[0].request.id and .request.headline == $f[0].request.headline
    and .request.description == $f[0].request.description and (.request | has("decided") | not)' "$ev" >/dev/null &&
  [ "$(jq -s length "$ev")" = 1 ]; then
  ok "pending fixture yields one unexpired schema-1 request-filed event with the fixture's text"
else
  bad "pending fixture did not yield a valid event: $(cat "$T/err" 2>/dev/null)"
fi

# 2. The ACTUAL candidate entry accepts that stdin. With no bus address it must stop at the
#    bus check, which runs after JSON decoding and request validation, and decide nothing.
out=$(env -i HOME="$T/home" PATH="/bin:/usr/bin" YOLO_BIN="$T/stub-yolo" "$YOLO" internal approval-notify <"$ev" 2>&1)
rc=$?
if [ "$rc" = 1 ] && echo "$out" | grep -q 'DBUS_SESSION_BUS_ADDRESS is missing' && [ ! -s "$T/stub.log" ]; then
  ok "candidate decodes and validates the event, then stops at the missing bus without running YOLO_BIN"
else
  bad "candidate did not accept the event (exit $rc): $out"
fi

# 3. The baseline interface (fixture environment, no stdin JSON) is not the candidate's input.
out=$(env -i HOME="$T/home" PATH="/bin:/usr/bin" YOLO_BIN="$T/stub-yolo" \
  YOLO_REQUEST_ID=r-ea4cd2bbc346fba003b008202228f942 YOLO_REQUEST_HEADLINE=x "$YOLO" internal approval-notify </dev/null 2>&1)
rc=$?
if [ "$rc" = 1 ] && echo "$out" | grep -q 'invalid request JSON' && [ ! -s "$T/stub.log" ]; then
  ok "candidate refuses the env-only baseline interface"
else
  bad "candidate accepted an env-only request (exit $rc): $out"
fi

# 4. Fixtures that are not a new pending request view are refused with a reason, and print nothing.
for id in state-allowed-once state-decided-elsewhere state-denied state-expired state-stale state-hook-failure circleci-run-list; do
  if "$HERE/request-event.sh" "$FIX/$id.json" >"$T/none" 2>"$T/err"; then
    bad "$id was turned into a pending event"
  elif [ "$?" != 3 ] || [ -s "$T/none" ] || ! grep -q "^skip $id: " "$T/err"; then
    bad "$id was refused without exit 3, an empty stdout and a 'skip $id: <reason>' line: $(cat "$T/err")"
  else
    ok "$id refused: $(cat "$T/err")"
  fi
done

# 5. A fixture's own non-pending request, fed unchanged, is refused by the candidate itself.
jq -c '{schema:1,event:"request-filed",source:.request.source,time:.request.filed,summary:"",request:.request}' \
  "$FIX/state-expired.json" >"$T/expired.json"
out=$(env -i HOME="$T/home" PATH="/bin:/usr/bin" YOLO_BIN="$T/stub-yolo" DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent \
  "$YOLO" internal approval-notify <"$T/expired.json" 2>&1)
rc=$?
if [ "$rc" = 1 ] && echo "$out" | grep -q 'expected an unexpired pending request' && [ ! -s "$T/stub.log" ]; then
  ok "candidate refuses an expired request before touching the bus"
else
  bad "candidate did not refuse an expired request (exit $rc): $out"
fi

# 6. The capture runner refuses --stdin-event without a distinct image directory.
out=$("$HERE/capture-linux.sh" --stdin-event --hook "$T/stub-yolo" --fixtures "$FIX" --out "$T/out" --run-dir "$T/run" 2>&1)
rc=$?
if [ "$rc" = 2 ] && echo "$out" | grep -q -- '--image-subdir'; then
  ok "capture-linux.sh --stdin-event demands --image-subdir"
else
  bad "capture-linux.sh --stdin-event ran without --image-subdir (exit $rc): $out"
fi


# 7. The committed manifest keys entries by renderer but images by path. A different renderer
#    aimed at a baseline's image directory is refused before anything is written.
CAP=$ROOT/docs/design/approval-experience/gallery/captures/linux
mkdir -p "$T/g/linux"
cp "$CAP/manifest.json" "$T/g/linux/manifest.json"
before=$(cksum <"$T/g/linux/manifest.json")
pre() { "$HERE/capture-linux.sh" --preflight --fixtures "$FIX" --out "$T/g/linux" --run-dir "$T/r" "$@" 2>&1; }
unchanged() { [ "$(cksum <"$T/g/linux/manifest.json")" = "$before" ] && [ "$(ls "$T/g/linux")" = manifest.json ] && [ ! -e "$T/r" ]; }
for daemon in swaync mako; do
  out=$(pre --hook "$T/stub-yolo" --renderer "some candidate" --daemon "$daemon")
  rc=$?
  if [ "$rc" = 2 ] && echo "$out" | grep -q -- '--image-subdir' && unchanged; then
    ok "a new renderer may not overwrite the $daemon baseline images"
  else
    bad "a new renderer was let onto the $daemon baseline images (exit $rc): $out"
  fi
done
out=$(pre --stdin-event --hook "$T/stub-yolo" --renderer "some candidate" --image-subdir mako)
rc=$?
if [ "$rc" = 2 ] && unchanged; then ok "--image-subdir may not name a baseline's directory either"; else bad "--image-subdir mako was accepted (exit $rc): $out"; fi
# A spelling that names a baseline's directory is that directory: the guard compares the canonical path.
for spelling in mako/ ./mako mako// .; do
  out=$(pre --stdin-event --hook "$T/stub-yolo" --renderer "some candidate" --image-subdir "$spelling")
  rc=$?
  if [ "$rc" = 2 ] && unchanged; then ok "--image-subdir '$spelling' is refused as the baseline directory it names"; else bad "--image-subdir '$spelling' was accepted (exit $rc): $out"; fi
done
# A symlink inside the output directory that names a baseline's directory is the same directory.
ln -s mako "$T/g/linux/alias"
out=$(pre --stdin-event --hook "$T/stub-yolo" --renderer "some candidate" --image-subdir alias)
rc=$?
rm -f "$T/g/linux/alias"
if [ "$rc" = 2 ] && unchanged; then ok "--image-subdir naming a symlink to a baseline directory is refused"; else bad "--image-subdir through a symlink to a baseline was accepted (exit $rc): $out"; fi
# A symlink inside the output directory that leads out of it names a directory this run does not own.
mkdir -p "$T/elsewhere" && ln -s "$T/elsewhere" "$T/g/linux/escape"
out=$(pre --stdin-event --hook "$T/stub-yolo" --renderer "some candidate" --image-subdir escape)
rc=$?
rm -f "$T/g/linux/escape"
if [ "$rc" = 2 ] && unchanged; then ok "--image-subdir through a symlink out of --out is refused"; else bad "--image-subdir escape was accepted (exit $rc): $out"; fi
out=$(pre --stdin-event --hook "$T/stub-yolo" --renderer "some candidate" --image-subdir some-candidate/mako --daemon mako)
rc=$?
if [ "$rc" = 0 ] && unchanged; then ok "a candidate in its own image directory passes preflight and writes nothing"; else bad "a namespaced candidate was refused (exit $rc): $out"; fi
out=$(pre --reference-baseline --daemon mako)
rc=$?
if [ "$rc" = 0 ] && unchanged; then ok "re-running a baseline under its own renderer label passes preflight"; else bad "the baseline's own rerun was refused (exit $rc): $out"; fi

# 8. Contradictory flags and a socket path that cannot work are refused with nothing created.
out=$("$HERE/capture-linux.sh" --hook "$T/stub-yolo" --reference-baseline --fixtures "$FIX" --out "$T/both" --run-dir "$T/bothrun" 2>&1)
rc=$?
if [ "$rc" = 2 ] && [ ! -e "$T/both" ] && [ ! -e "$T/bothrun" ]; then ok "--hook with --reference-baseline is refused before any directory exists"; else bad "--hook with --reference-baseline (exit $rc) left files: $out"; fi
long=$T/$(printf 'x%.0s' $(seq 90))
out=$("$HERE/capture-linux.sh" --reference-baseline --daemon mako --fixtures "$FIX" --out "$T/longout" --run-dir "$long" 2>&1)
rc=$?
if [ "$rc" = 2 ] && echo "$out" | grep -q -- '--run-dir' && [ ! -e "$long" ] && [ ! -e "$T/longout" ]; then
  ok "a run directory too long for a socket path is refused, naming --run-dir"
else
  bad "a too-long run directory was accepted (exit $rc): $out"
fi

# 9. A NotificationClosed reason is its second argument: id 2 closed for reason 3 is not a dismissal.
printf 'signal sender=:1.2 member=NotificationClosed\n   uint32 2\n   uint32 3\n' >"$T/closed.dbus"
got=$(awk "$(sed -n 's/^closed_reason() { awk .\(.*\). "\$1"; }$/\1/p' "$HERE/measure-lifecycle.sh")" "$T/closed.dbus")
if [ "$got" = 3 ]; then ok "closed_reason reads the reason, not the id"; else bad "closed_reason read [$got] for id 2, reason 3"; fi

[ "$FAILS" = 0 ] || { echo "$FAILS failed" >&2; exit 1; }
echo "all passed"
