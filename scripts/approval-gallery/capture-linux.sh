#!/bin/bash
# shellcheck disable=SC2012,SC2016
# Capture the owner's approval toast as a real Linux desktop notification.
#
# Brings up a private D-Bus session, a headless sway (1280x800, scale 1) and
# swaync with the owner's config ({"timeout": 3}, default theme), runs the
# on_request hook once per fixture that has a `hook_env` object, screenshots
# the toast with grim, crops it, and dismisses it.
#
# usage: capture-linux.sh --hook <on_request script> --fixtures <dir> --out <dir>
#                         [--run-dir <short dir for sockets>] [--lifecycle]
#
# <out> receives <fixture-id>.png and manifest.json (paths in the manifest are
# relative to the gallery directory, as captures/linux/<id>.png, matching the
# macOS manifest). With --lifecycle, also runs measure-lifecycle.sh for swaync
# and mako into <run-dir>/lifecycle-raw (raw evidence for FINDINGS).
#
# The hook runs with a stub `yolo` first on PATH that only logs its arguments
# to <run-dir>/stub-yolo.log; a real `yolo approve` is never run. Dependencies
# come from nixpkgs via `nix shell` if they are not already on PATH.
set -eu

HOOK="" FIXTURES="" OUT="" RUN="" LIFECYCLE=0
while [ $# -gt 0 ]; do
  case $1 in
    --hook) HOOK=$2; shift 2 ;;
    --fixtures) FIXTURES=$2; shift 2 ;;
    --out) OUT=$2; shift 2 ;;
    --run-dir) RUN=$2; shift 2 ;;
    --lifecycle) LIFECYCLE=1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [ -z "$HOOK" ] || [ -z "$FIXTURES" ] || [ -z "$OUT" ]; then
  echo "usage: $0 --hook <script> --fixtures <dir> --out <dir> [--run-dir <dir>] [--lifecycle]" >&2
  exit 2
fi
HERE=$(cd "$(dirname "$0")" && pwd)
HOOK=$(realpath "$HOOK")
FIXTURES=$(realpath "$FIXTURES")
mkdir -p "$OUT"
OUT=$(realpath "$OUT")
if [ -z "$RUN" ]; then
  RUN=$(mktemp -d "${YOLO_DURABLE_DIR:-${TMPDIR:-/tmp}}/agal.XXXXXX")
fi
mkdir -p "$RUN"
RUN=$(realpath "$RUN")

# Re-exec once inside a private session bus, with the tools on PATH.
if [ -z "${AGAL_INNER:-}" ]; then
  need=""
  for t in sway swaync grim notify-send dbus-run-session jq magick wlrctl rg gdbus; do
    command -v "$t" >/dev/null 2>&1 || need=1
  done
  args=("$0" --hook "$HOOK" --fixtures "$FIXTURES" --out "$OUT" --run-dir "$RUN")
  [ "$LIFECYCLE" = 1 ] && args+=(--lifecycle)
  export AGAL_INNER=1
  if [ -n "$need" ]; then
    exec nix shell nixpkgs#sway nixpkgs#swaynotificationcenter nixpkgs#mako nixpkgs#grim \
      nixpkgs#libnotify nixpkgs#dbus nixpkgs#jq nixpkgs#imagemagick nixpkgs#wlrctl \
      nixpkgs#ripgrep nixpkgs#glib nixpkgs#optipng \
      -c bash -c 'conf=$(dirname "$(dirname "$(command -v dbus-daemon)")")/share/dbus-1/session.conf; exec dbus-run-session --config-file="$conf" -- "$@"' _ "${args[@]}"
  fi
  conf=$(dirname "$(dirname "$(command -v dbus-daemon)")")/share/dbus-1/session.conf
  exec dbus-run-session --config-file="$conf" -- "${args[@]}"
fi

W=$RUN/work
rm -rf "$W"
mkdir -p "$W/stub" "$W/xdg" "$W/cfg/swaync"
chmod 700 "$W/xdg"
export XDG_RUNTIME_DIR=$W/xdg XDG_CONFIG_HOME=$W/cfg GTK_A11Y=none
export WLR_BACKENDS=headless WLR_LIBINPUT_NO_DEVICES=1 WLR_RENDERER=pixman
STUBLOG=$W/stub-yolo.log
: >"$STUBLOG"
printf '#!/bin/sh\necho "yolo $*" >>"%s"\n' "$STUBLOG" >"$W/stub/yolo"
chmod +x "$W/stub/yolo"

cat >"$W/sway.conf" <<'EOC'
output HEADLESS-1 resolution 1280x800 scale 1
output * bg #000000 solid_color
default_border none
bar {
    mode invisible
}
EOC
echo '{"timeout": 3}' >"$W/cfg/swaync/config.json"

sway -c "$W/sway.conf" >"$W/sway.log" 2>&1 &
SWAYPID=$!
trap 'kill ${DPID:-} "$SWAYPID" 2>/dev/null || true' EXIT
for _ in $(seq 100); do
  ls "$XDG_RUNTIME_DIR"/wayland-[0-9] >/dev/null 2>&1 && break
  sleep 0.2
done
WAYLAND_DISPLAY=$(basename "$(ls "$XDG_RUNTIME_DIR"/wayland-[0-9] | head -1)")
export WAYLAND_DISPLAY
SWAYSOCK=$(ls "$XDG_RUNTIME_DIR"/sway-ipc.*.sock | head -1)
export SWAYSOCK
swaync >"$W/swaync.log" 2>&1 &
DPID=$!
for _ in $(seq 100); do
  gdbus call --session -d org.freedesktop.Notifications -o /org/freedesktop/Notifications \
    -m org.freedesktop.Notifications.GetServerInformation >"$W/serverinfo.txt" 2>&1 && break
  sleep 0.2
done

shot() { grim "$1"; }
mean() { magick "$1" -format '%[fx:mean]' info: 2>/dev/null; }

COMMIT=$(git -C "$HERE" rev-parse HEAD 2>/dev/null || echo unknown)
# Generic on purpose: the kernel and distribution say more about the capturing machine than
# about the rendering, which the daemon, toolkit and font versions below already pin.
OSSTR="Linux userland in a container"
SWAYNC_STORE=$(dirname "$(dirname "$(readlink -f "$(command -v swaync)")")")
SWAYNC_VER=$(basename "$SWAYNC_STORE" | sed 's/^[a-z0-9]*-//')
SWAY_VER=$(sway --version | head -1 || true)
NS_VER=$(notify-send --version | head -1 || true)
GTK_VER=$(nix-store -qR "$SWAYNC_STORE" 2>/dev/null | sed -n 's#.*/[a-z0-9]*-\(gtk4-[0-9.]*\)$#\1#p' | head -1 || true)
FONT=$(fc-match -s sans 2>/dev/null | head -1 || true)
[ -n "$FONT" ] || FONT="fontconfig default sans (nix: $(nix-store -qR "$SWAYNC_STORE" 2>/dev/null | sed -n 's#.*/[a-z0-9]*-\(dejavu-fonts[a-z-]*-[0-9.]*\)$#\1#p' | head -1))"
CAPTURED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
{
  echo "sway: $(readlink -f "$(command -v sway)")"
  echo "swaync: $SWAYNC_STORE"
  echo "grim: $(readlink -f "$(command -v grim)")"
  echo "notify-send: $(readlink -f "$(command -v notify-send)")"
  echo "font: $FONT"
  echo "gtk: ${GTK_VER:-unknown}"
} >"$OUT/tools.txt"

FAILED=""
ENTRIES=$W/entries.jsonl
: >"$ENTRIES"
for f in "$FIXTURES"/*.json; do
  jq -e '.hook_env | type == "object"' "$f" >/dev/null 2>&1 || { echo "skip (no hook_env): $f"; continue; }
  # A request whose hook could not show anything has no toast to capture.
  if jq -e '(.request.hook_failure // "") != ""' "$f" >/dev/null 2>&1; then echo "skip (the hook showed nothing): $f"; continue; fi
  state=$(jq -r .state "$f")
  note="Headless sway with swaync default theme: the owner's script and daemon config, not the owner's desktop pixels."
  [ "$state" = pending ] || note="$note Shows the toast as filed; the $state outcome is not something a toast renders."
  id=$(jq -r .id "$f")
  echo "== $id"
  png=$OUT/$id.png
  full=$W/$id.full.png
  # Hook environment: only the fixture's YOLO_REQUEST_* values (one key at a time, since values
  # can hold newlines), a throwaway HOME, and what reaching the daemon needs, so the hook
  # cannot find the real yolo or its state by any path.
  (
    args=(HOME="$W/home" PATH="$W/stub:$PATH" DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS"
      WAYLAND_DISPLAY="$WAYLAND_DISPLAY" XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR")
    for k in $(jq -r '.hook_env | keys[] | select(startswith("YOLO_REQUEST_"))' "$f"); do
      args+=("$k=$(jq -j --arg k "$k" '.hook_env[$k]' "$f")")
    done
    mkdir -p "$W/home"
    if [ -x "$HOOK" ]; then exec env -i "${args[@]}" "$HOOK"; else exec env -i "${args[@]}" sh "$HOOK"; fi
  ) >"$W/$id.hook.out" 2>&1 &
  HP=$!
  ok=""
  prev=""
  for _ in $(seq 60); do
    shot "$full"
    m=$(mean "$full")
    if [ -n "$m" ] && [ "$m" != 0 ]; then
      sum=$(cksum <"$full")
      if [ "$sum" = "$prev" ]; then ok=1; break; fi
      prev=$sum
    else
      prev=""
    fi
    sleep 0.4
  done
  if [ -z "$ok" ]; then
    echo "  FAILED: no stable notification appeared" >&2
    FAILED="$FAILED $id"
    rm -f "$png"
    kill -INT "$HP" 2>/dev/null || true
    wait "$HP" 2>/dev/null || true
    continue
  fi
  # Crop to the toast: the background is solid black, so trim to the non-black box.
  magick "$full" -fuzz 1% -trim +repage -bordercolor black -border 10 "$png"
  optipng -quiet "$png" 2>/dev/null || true
  # Dismiss like a user would (close-latest): notify-send prints nothing, the hook answers nothing.
  swaync-client --close-latest >/dev/null 2>&1
  for _ in $(seq 40); do kill -0 "$HP" 2>/dev/null || break; sleep 0.25; done
  if kill -0 "$HP" 2>/dev/null; then kill -INT "$HP" 2>/dev/null || true; fi
  wait "$HP" 2>/dev/null || true
  jq -cn --arg id "$id" --arg os "$OSSTR" --arg dv "$SWAYNC_VER" \
    --arg theme "swaync default CSS, GTK theme default (${GTK_VER:-gtk4}; no gsettings theme set)" \
    --arg at "$CAPTURED" --arg commit "$COMMIT" --arg comp "sway" --arg cv "$SWAY_VER" \
    --arg lv "$NS_VER" --arg fonts "$FONT" --arg note "$note" \
    '{id:$id,entry:{file:("captures/linux/"+$id+".png"),renderer:"owner baseline: on_request script via notify-send to swaync",tested:"visual",
      note:$note,
      provenance:{os:$os,daemon:"swaync",daemon_version:$dv,theme:$theme,scale:1,captured_at:$at,commit:$commit,
        compositor:$comp,compositor_version:$cv,compositor_mode:"headless (WLR_BACKENDS=headless, pixman)",
        libnotify_version:$lv,resolution:"1280x800",fonts:$fonts,
        crop:"trimmed to the non-black bounding box plus 10px, from a full-output grim screenshot",
        daemon_config:"{\"timeout\": 3}"}}}' >>"$ENTRIES"
done

jq -s '{schema:1,platform:"linux",captures:(reduce .[] as $e ({}; .[$e.id] += [$e.entry]))}' "$ENTRIES" >"$OUT/manifest.json"
if [ -s "$STUBLOG" ]; then
  echo "FAILED: the hook ran yolo during a capture (nothing presses a button here):" >&2
  cat "$STUBLOG" >&2
  exit 1
fi

if [ "$LIFECYCLE" = 1 ]; then
  kill "$DPID" "$SWAYPID" 2>/dev/null || true
  for d in swaync mako; do
    bash "$HERE/measure-lifecycle.sh" "$d" "$RUN/lifecycle-raw" "$RUN/lc" || echo "lifecycle $d failed" >&2
  done
fi

if [ -n "$FAILED" ]; then
  echo "FAILED captures:$FAILED (their manifest entries and images are absent)" >&2
  exit 1
fi
