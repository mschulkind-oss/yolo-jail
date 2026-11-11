#!/bin/bash
# shellcheck disable=SC2012,SC2016
# Capture a supplied hook or PUBLIC reference call as a real Linux notification.
#
# Brings up a private D-Bus session, a headless sway (1280x800, scale 1) and
# swaync with a test config ({"timeout": 3}, default theme), runs the
# on_request hook once per fixture that has a `hook_env` object, screenshots
# the toast with grim, crops it, and dismisses it.
#
# usage: capture-linux.sh --hook <on_request script> --fixtures <dir> --out <dir>
#                         [--daemon swaync|mako] [--hook-source <source identity>]
#                         [--renderer <label>] [--run-dir <durable sockets dir>] [--lifecycle]
# Or use --reference-baseline instead of --hook: reconstructs the PUBLIC documented call.
#
# <out> receives <fixture-id>.png (mako/<fixture-id>.png for mako) and manifest.json (paths in the manifest are
# relative to the gallery directory, as captures/linux/<id>.png, matching the
# macOS manifest). With --lifecycle, also runs measure-lifecycle.sh for swaync
# and mako into <run-dir>/lifecycle-raw (raw evidence for FINDINGS).
#
# The hook runs with a stub `yolo` first on PATH that only logs its arguments
# to <run-dir>/work/stub-yolo.log; a real `yolo approve` is never run. Dependencies
# come from nixpkgs via `nix shell` if they are not already on PATH.
set -eu

HOOK="" FIXTURES="" OUT="" RUN="" LIFECYCLE=0 DAEMON=swaync REFERENCE=0 RENDERER=""
SOURCE="operator-supplied hook (identity unspecified)"
while [ $# -gt 0 ]; do
  case $1 in
    --hook) HOOK=$2; shift 2 ;;
    --reference-baseline) REFERENCE=1; SOURCE="native-notification-gallery.md section 2.1 documented Linux notify call; reference reconstruction, not private owner hook"; shift ;;
    --renderer) RENDERER=$2; shift 2 ;;
    --hook-source) SOURCE=$2; shift 2 ;;
    --daemon) DAEMON=$2; shift 2 ;;
    --fixtures) FIXTURES=$2; shift 2 ;;
    --out) OUT=$2; shift 2 ;;
    --run-dir) RUN=$2; shift 2 ;;
    --lifecycle) LIFECYCLE=1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
case $DAEMON in swaync|mako) ;; *) echo "unsupported daemon: $DAEMON; use --daemon swaync or mako" >&2; exit 2 ;; esac
if { [ -z "$HOOK" ] && [ "$REFERENCE" = 0 ]; } || [ -z "$FIXTURES" ] || [ -z "$OUT" ]; then
  echo "usage: $0 --hook <script> --fixtures <dir> --out <dir> [--daemon swaync|mako] [--hook-source <identity>] [--renderer <label>] [--run-dir <dir>] [--lifecycle]; or use --reference-baseline instead of --hook" >&2
  exit 2
fi
HERE=$(cd "$(dirname "$0")" && pwd)
[ -z "$HOOK" ] || HOOK=$(realpath "$HOOK")
FIXTURES=$(realpath "$FIXTURES")
mkdir -p "$OUT"
OUT=$(realpath "$OUT")
if [ -z "$RUN" ]; then
  mkdir -p "${YOLO_DURABLE_DIR:-$PWD/.yolo/durable}"
  RUN=$(mktemp -d "${YOLO_DURABLE_DIR:-$PWD/.yolo/durable}/agal.XXXXXX")
fi
mkdir -p "$RUN"
RUN=$(realpath "$RUN")

if [ "$REFERENCE" = 1 ]; then
  [ -z "$HOOK" ] || { echo "choose --hook or --reference-baseline, not both" >&2; exit 2; }
  HOOK=$RUN/reference-hook.sh
  cat >"$HOOK" <<'EOH'
#!/bin/sh
# Reconstruction of the PUBLIC documented Linux call, not the owner's private script.
notify-send -u critical -a yolo -A once="Allow once" -A hour="Allow 1h" -A deny=Deny \
  "yolo: $YOLO_REQUEST_HEADLINE" "${YOLO_REQUEST_DESCRIPTION_MARKUP:-$YOLO_REQUEST_DESCRIPTION}"
EOH
fi

if [ -z "$RENDERER" ]; then
  if [ "$REFERENCE" = 1 ]; then RENDERER="public reference baseline: documented notify-send call to $DAEMON";
  else RENDERER="operator-supplied hook to $DAEMON"; fi
fi

# Re-exec once inside a private session bus, with the tools on PATH.
if [ -z "${AGAL_INNER:-}" ]; then
  need=""
  for t in sway swaync mako grim notify-send dbus-run-session jq magick wlrctl rg gdbus optipng wayland-scanner; do
    command -v "$t" >/dev/null 2>&1 || need=1
  done
  args=("$0" --hook "$HOOK" --hook-source "$SOURCE" --renderer "$RENDERER" --daemon "$DAEMON" --fixtures "$FIXTURES" --out "$OUT" --run-dir "$RUN")
  [ "$LIFECYCLE" = 1 ] && args+=(--lifecycle)
  export AGAL_INNER=1
  if [ -n "$need" ]; then
    exec nix shell nixpkgs#sway nixpkgs#swaynotificationcenter nixpkgs#mako nixpkgs#grim \
      nixpkgs#libnotify nixpkgs#dbus nixpkgs#jq nixpkgs#imagemagick nixpkgs#wlrctl \
      nixpkgs#ripgrep nixpkgs#glib nixpkgs#optipng nixpkgs#wayland-scanner \
      -c bash -c 'conf=$(dirname "$(dirname "$(command -v dbus-daemon)")")/share/dbus-1/session.conf; exec dbus-run-session --config-file="$conf" -- "$@"' _ "${args[@]}"
  fi
  conf=$(dirname "$(dirname "$(command -v dbus-daemon)")")/share/dbus-1/session.conf
  exec dbus-run-session --config-file="$conf" -- "${args[@]}"
fi

IMAGEOUT=$OUT
[ "$DAEMON" != mako ] || IMAGEOUT=$OUT/mako
mkdir -p "$IMAGEOUT"
RELDIR=$(basename "$OUT")
[ "$DAEMON" != mako ] || RELDIR=$RELDIR/mako

W=$RUN/work
rm -rf "$W"
mkdir -p "$W/stub" "$W/xdg" "$W/cfg/$DAEMON"
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
[ "$DAEMON" != swaync ] || echo '{"timeout": 3}' >"$W/cfg/swaync/config.json"

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
if [ "$DAEMON" = mako ]; then
  printf 'default-timeout=0\n' >"$W/cfg/mako/config"
fi
"$DAEMON" >"$W/$DAEMON.log" 2>&1 &
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
SWAYNC_STORE=$(dirname "$(dirname "$(readlink -f "$(command -v "$DAEMON")")")")
SWAYNC_VER=$(basename "$SWAYNC_STORE" | sed 's/^[a-z0-9]*-//')
SWAY_VER=$(sway --version | head -1 || true)
NS_VER=$(notify-send --version | head -1 || true)
GTK_VER=$(nix-store -qR "$SWAYNC_STORE" 2>/dev/null | sed -n 's#.*/[a-z0-9]*-\(gtk4-[0-9.]*\)$#\1#p' | head -1 || true)
FONT=$(fc-match -s sans 2>/dev/null | head -1 || true)
[ -n "$FONT" ] || FONT="fontconfig default sans (nix: $(nix-store -qR "$SWAYNC_STORE" 2>/dev/null | sed -n 's#.*/[a-z0-9]*-\(dejavu-fonts[a-z-]*-[0-9.]*\)$#\1#p' | head -1))"
CAPTURED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
{
  echo "sway: $(readlink -f "$(command -v sway)")"
  echo "daemon: $SWAYNC_STORE"
  echo "hook source: $SOURCE"
  echo "fixture commit: $COMMIT"
  echo "grim: $(readlink -f "$(command -v grim)")"
  echo "notify-send: $(readlink -f "$(command -v notify-send)")"
  echo "font: $FONT"
  echo "gtk: ${GTK_VER:-unknown}"
} >"$IMAGEOUT/tools.txt"

FAILED=""
ENTRIES=$W/entries.jsonl
: >"$ENTRIES"
for f in "$FIXTURES"/*.json; do
  jq -e '.hook_env | type == "object"' "$f" >/dev/null 2>&1 || { echo "skip (no hook_env): $f"; continue; }
  # A request whose hook could not show anything has no toast to capture.
  if jq -e '(.request.hook_failure // "") != ""' "$f" >/dev/null 2>&1; then echo "skip (the hook showed nothing): $f"; continue; fi
  state=$(jq -r .state "$f")
  note="Native headless sway with $DAEMON test config/default theme; public reference or supplied hook, not the owner's desktop pixels. Baseline only, not production notifier repair evidence."
  [ "$state" = pending ] || note="$note Shows the toast as filed; the $state outcome is not something a toast renders."
  id=$(jq -r .id "$f")
  echo "== $id"
  png=$IMAGEOUT/$id.png
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
  if [ "$DAEMON" = swaync ]; then swaync-client --close-latest >/dev/null 2>&1; else makoctl dismiss >/dev/null 2>&1; fi
  for _ in $(seq 40); do kill -0 "$HP" 2>/dev/null || break; sleep 0.25; done
  if kill -0 "$HP" 2>/dev/null; then kill -INT "$HP" 2>/dev/null || true; fi
  wait "$HP" 2>/dev/null || true
  jq -cn --arg id "$id" --arg os "$OSSTR" --arg dv "$SWAYNC_VER" \
    --arg theme "$DAEMON default theme (${GTK_VER:-no GTK}; no gsettings theme set)" \
    --arg at "$CAPTURED" --arg commit "$COMMIT" --arg comp "sway" --arg cv "$SWAY_VER" \
    --arg lv "$NS_VER" --arg fonts "$FONT" --arg note "$note" \
    --arg daemon "$DAEMON" --arg source "$SOURCE" --arg dir "$RELDIR" \
    --arg config "$([ "$DAEMON" = swaync ] && echo '{"timeout": 3}' || echo 'default-timeout=0')" \
    --arg renderer "$RENDERER" \
    '{id:$id,entry:{file:("captures/"+$dir+"/"+$id+".png"),renderer:$renderer,tested:"visual",
      note:$note,
      provenance:{os:$os,daemon:$daemon,daemon_version:$dv,theme:$theme,scale:1,captured_at:$at,commit:$commit,
        compositor:$comp,compositor_version:$cv,compositor_mode:"headless (WLR_BACKENDS=headless, pixman)",
        libnotify_version:$lv,resolution:"1280x800",fonts:$fonts,
        crop:"trimmed to the non-black bounding box plus 10px, from a full-output grim screenshot",
        daemon_config:$config,hook_source:$source}}}' >>"$ENTRIES"
done

if [ -f "$OUT/manifest.json" ]; then cp "$OUT/manifest.json" "$W/previous.json";
else echo '{"schema":1,"platform":"linux","captures":{}}' >"$W/previous.json"; fi
# Replace this renderer's entire run, preserving other daemons/baselines.
jq -s --arg renderer "$RENDERER" --slurpfile previous "$W/previous.json" '
  {schema:1,platform:"linux",captures:
    (reduce .[] as $e
      ($previous[0].captures | with_entries(.value |= map(select(.renderer != $renderer)))
       | with_entries(select(.value | length > 0)); .[$e.id] += [$e.entry]))}' \
  "$ENTRIES" >"$OUT/manifest.json"
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
