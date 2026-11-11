#!/bin/bash
# shellcheck disable=SC2012
# Measures how a Linux notification daemon treats the owner's approval toast
# (notify-send -u critical with three actions): the D-Bus traffic, dismissal,
# action invocation, pointer click, SIGINT/SIGKILL of notify-send, expiry, and
# (swaync) Do Not Disturb. Raw evidence lands in <out-dir>/<daemon>/.
#
# usage: measure-lifecycle.sh <swaync|mako> <out-dir> <run-dir>
# Must run under a private session bus with sway, the daemon, grim, libnotify,
# dbus, imagemagick, wlrctl, ripgrep and glib on PATH; see capture-linux.sh,
# which re-executes itself under `dbus-run-session` + `nix shell`.
# <run-dir> holds sockets, so its path must be short (< ~80 chars).
set -u
[ "$#" -ge 3 ] && [ "$#" -le 4 ] || { echo "usage: $0 <swaync|mako> <out-dir> <short run-dir> [--pointer-only|--pointer-action]" >&2; exit 2; }
case $1 in swaync|mako) ;; *) echo "use swaync or mako" >&2; exit 2 ;; esac
MODE=${4:-all}
case $MODE in all|--pointer-only|--pointer-action) ;; *) echo "unknown mode: $MODE; use --pointer-only or --pointer-action" >&2; exit 2 ;; esac
if [ "$MODE" = --pointer-action ] && [ "$1" != swaync ]; then
  echo "--pointer-action targets swaync buttons only; use --pointer-only for mako" >&2; exit 2
fi
DAEMON=$1
OUT=$2
RUN=$3
O=$OUT/$DAEMON
rm -rf "$O"
mkdir -p "$O"
export XDG_RUNTIME_DIR=$RUN/$DAEMON
rm -rf "$XDG_RUNTIME_DIR"
mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
export WLR_BACKENDS=headless WLR_LIBINPUT_NO_DEVICES=1 WLR_RENDERER=pixman
cat >"$O/sway.conf" <<'EOC'
output HEADLESS-1 resolution 1280x800 scale 1
output * bg #000000 solid_color
seat seat0 fallback true
seat seat0 attach *
default_border none
bar {
    mode invisible
}
EOC
sway -c "$O/sway.conf" >"$O/sway.log" 2>&1 &
SWAYPID=$!
for _ in $(seq 50); do
  ls "$XDG_RUNTIME_DIR"/wayland-[0-9] >/dev/null 2>&1 && break
  sleep 0.2
done
WAYLAND_DISPLAY=$(basename "$(ls "$XDG_RUNTIME_DIR"/wayland-[0-9] | head -1)")
export WAYLAND_DISPLAY
SWAYSOCK=$(ls "$XDG_RUNTIME_DIR"/sway-ipc.*.sock | head -1)
export SWAYSOCK
export XDG_CONFIG_HOME=$O/cfg
mkdir -p "$XDG_CONFIG_HOME/$DAEMON"
export GTK_A11Y=none
if [ "$DAEMON" = swaync ]; then
  echo '{"timeout": 3}' >"$XDG_CONFIG_HOME/swaync/config.json"
  swaync >"$O/daemon.log" 2>&1 &
else
  if [ "$MODE" != all ]; then printf 'default-timeout=0\n'; else printf 'default-timeout=3000\n'; fi >"$XDG_CONFIG_HOME/mako/config"
  mako >"$O/daemon.log" 2>&1 &
fi
DPID=$!
MONPID="" SENDPID=""
# An interrupted run leaves nothing behind: the monitor, the daemon, sway, and any notifier.
trap 'for p in $SENDPID; do pkill -P "$p" 2>/dev/null; done; kill ${POINTERPID:-} $MONPID "$DPID" "$SWAYPID" 2>/dev/null' EXIT
for _ in $(seq 50); do
  gdbus call --session -d org.freedesktop.Notifications -o /org/freedesktop/Notifications \
    -m org.freedesktop.Notifications.GetServerInformation >"$O/serverinfo.txt" 2>&1 && break
  sleep 0.2
done
gdbus call --session -d org.freedesktop.Notifications -o /org/freedesktop/Notifications \
  -m org.freedesktop.Notifications.GetCapabilities >"$O/capabilities.txt" 2>&1

shot() { grim "$O/$1.png"; }
visible() { magick "$O/$1.png" -format '%[fx:mean]' info: 2>/dev/null; }
wait_visible() {
  for _ in $(seq 40); do
    shot "$1"
    m=$(visible "$1")
    if [ -n "$m" ] && [ "$m" != 0 ]; then
      sum=$(cksum <"$O/$1.png")
      if [ "$sum" = "${prev:-}" ]; then return 0; fi
      prev=$sum
    fi
    sleep 0.25
  done
  return 1
}
wait_gone() {
  for _ in $(seq 40); do
    shot "$1"
    [ "$(visible "$1")" = 0 ] && return 0
    sleep 0.25
  done
  return 1
}
await_log() {
  # Poll observed events, with a generous hang guard; elapsed time is never a pass condition.
  timeout 120 bash -c 'until rg -q "$1" "$2"; do sleep 0.1; done' _ "$1" "$2"
}
mon_start() {
  dbus-monitor --session "type='method_call',interface='org.freedesktop.Notifications'" \
    "type='signal',interface='org.freedesktop.Notifications'" "type='method_return'" \
    "type='error'" >"$O/$1.dbus" 2>&1 &
  MONPID=$!
  await_log "NameAcquired" "$O/$1.dbus"
}
mon_stop() {
  kill "$MONPID" 2>/dev/null
  wait "$MONPID" 2>/dev/null
}
send() { # name: runs notify-send exactly as the owner's hook does, in the background
  n=$1
  (
    notify-send -u critical -a yolo -A once="Allow once" -A hour="Allow 1h" -A deny=Deny \
      "yolo: rerun failed jobs" $'rerun failed jobs of workflow 8f14e45f\nworkflow: main (failed)' \
      >"$O/$n.stdout" 2>"$O/$n.stderr"
    echo $? >"$O/$n.exit"
  ) &
  SENDPID=$!
}
waitexit() {
  for _ in $(seq 60); do
    [ -s "$O/$1.exit" ] && return 0
    sleep 0.25
  done
  return 1
}
count() { if [ "$DAEMON" = swaync ]; then swaync-client -c; else makoctl list | rg -c '^Notification'; fi; }
# lastns is the notify-send of the latest send, never another one in the same PID namespace.
lastns() { pgrep -n -x -P "$SENDPID" notify-send; }


# A single device must remain alive from absolute motion through release. Separate
# wlrctl invocations remove pointer capability between those steps in headless sway.
pointer_client() {
  if [ "${POINTERBUILT:-0}" = 0 ]; then
  nix build --out-link "$RUN/wayland" nixpkgs#wayland.dev >/dev/null || return 1
  nix build --out-link "$RUN/pointer-protocols" nixpkgs#wlr-protocols >/dev/null || return 1
  xml=$RUN/pointer-protocols/share/wlr-protocols/unstable/wlr-virtual-pointer-unstable-v1.xml
  wayland-scanner client-header "$xml" "$RUN/pointer.h"
  wayland-scanner private-code "$xml" "$RUN/pointer-protocol.c"
  cat >"$RUN/pointer.c" <<'EOC'
#include <assert.h>
#include <stdint.h>
#include <unistd.h>
#include <time.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wayland-client.h>
#include "pointer.h"
static struct zwlr_virtual_pointer_manager_v1 *manager;
static struct wl_seat *seat;
static uint32_t now(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC, &t); return t.tv_sec * 1000 + t.tv_nsec / 1000000; }
static void global(void *data, struct wl_registry *r, uint32_t n,
                   const char *name, uint32_t version) {
  (void)data; (void)version;
  if (!strcmp(name, "zwlr_virtual_pointer_manager_v1"))
    manager = wl_registry_bind(r, n, &zwlr_virtual_pointer_manager_v1_interface, 1);
  if (!strcmp(name, "wl_seat") && !seat)
    seat = wl_registry_bind(r, n, &wl_seat_interface, 1);
}
static void removed(void *data, struct wl_registry *r, uint32_t n) {
  (void)data; (void)r; (void)n;
}
int main(int argc, char **argv) {
  assert(argc == 3);
  struct wl_display *d = wl_display_connect(NULL);
  assert(d);
  struct wl_registry *r = wl_display_get_registry(d);
  const struct wl_registry_listener listener = {global, removed};
  wl_registry_add_listener(r, &listener, NULL);
  assert(wl_display_roundtrip(d) >= 0);
  assert(manager && seat);
  struct zwlr_virtual_pointer_v1 *p =
    zwlr_virtual_pointer_manager_v1_create_virtual_pointer(manager, seat);
  assert(wl_display_roundtrip(d) >= 0);
  zwlr_virtual_pointer_v1_motion_absolute(p, now(), atoi(argv[1]), atoi(argv[2]), 1280, 800);
  zwlr_virtual_pointer_v1_frame(p);
  assert(wl_display_roundtrip(d) >= 0);
  zwlr_virtual_pointer_v1_button(p, now(), 0x110, WL_POINTER_BUTTON_STATE_PRESSED);
  zwlr_virtual_pointer_v1_frame(p);
  assert(wl_display_roundtrip(d) >= 0);
  puts("pressed"); fflush(stdout);
  assert(getchar() != EOF);
  zwlr_virtual_pointer_v1_button(p, now(), 0x110, WL_POINTER_BUTTON_STATE_RELEASED);
  zwlr_virtual_pointer_v1_frame(p);
  assert(wl_display_roundtrip(d) >= 0);
  puts("absolute motion, press, release: compositor roundtrips acknowledged");
  fflush(stdout);
  pause(); /* Keep pointer capability until the daemon has handled release. */
  zwlr_virtual_pointer_v1_destroy(p);
  wl_display_roundtrip(d);
  wl_display_disconnect(d);
  return 0;
}
EOC
  lib=$(sed -n 's/^prefix=//p' "$RUN/wayland-dev/lib/pkgconfig/wayland-client.pc")
  lib=$lib/lib
  cc -I"$RUN/wayland-dev/include" -I"$RUN" "$RUN/pointer.c" "$RUN/pointer-protocol.c" \
    -L"$lib" -Wl,-rpath,"$lib" -lwayland-client -o "$RUN/body-click" || return 1
  POINTERBUILT=1
  fi
  [ "$1" != --build ] || return 0
  rm -f "$RUN/pointer-release"
  mkfifo "$RUN/pointer-release"
  exec 9<>"$RUN/pointer-release"
  "$RUN/body-click" "$1" "$2" <&9 &
  POINTERPID=$!
  await_log '^pressed$' "$O/pointer.cmd" || return 1
  # A daemon main-loop roundtrip is a stimulus barrier, not an elapsed-time assertion.
  gdbus call --session -d org.freedesktop.Notifications -o /org/freedesktop/Notifications \
    -m org.freedesktop.Notifications.GetServerInformation
  echo release >&9
  exec 9>&-

}

if [ "$MODE" != all ]; then
  pointer_client --build || exit 1
  echo "== real virtual-pointer $MODE: $DAEMON"
  mon_start pointer || exit 1
  send pointer
  wait_visible pointer_before || { echo "no rendered toast; inspect $O/daemon.log" >&2; exit 1; }
  # Sway's cursor starts at the output center. Move to the notification's measured
  # non-black bounding box. The optional action control targets the first button.
  box=$(magick "$O/pointer_before.png" -fuzz 1% -format '%@' info:)
  echo "toast bounding box=$box" >"$O/pointer.cmd"
  width=${box%%x*}
  offset=$((width-30))
  x=$(echo "$box" | sed -E 's/.*\+([0-9]+)\+([0-9]+)$/\1/')
  y=$(echo "$box" | sed -E 's/.*\+([0-9]+)\+([0-9]+)$/\2/')
  dy=45
  if [ "$MODE" = --pointer-action ]; then offset=80; dy=95; fi
  pointer_client "$((x+offset))" "$((y+dy))" >>"$O/pointer.cmd" 2>&1 || exit 1
  await_log 'roundtrips acknowledged' "$O/pointer.cmd" || exit 1
  grim -c "$O/pointer-delivered.png"
  swaymsg -t get_seats >"$O/pointer-seats-live.json"
  await_log 'member=NotificationClosed' "$O/pointer.dbus" || {
    shot pointer_after
    echo "pointer stimulus did not close; inspect $O/pointer.cmd and pointer.dbus, then verify input on this toolkit before claiming a semantic outcome" >&2
    kill -INT "$(lastns)" 2>/dev/null || true
    exit 1
  }
  kill "$POINTERPID" 2>/dev/null || true
  waitexit pointer || exit 1
  wait_gone pointer_after || exit 1
  mon_stop
  if [ "$MODE" = --pointer-only ]; then
    if rg -q 'member=ActionInvoked' "$O/pointer.dbus" || [ -s "$O/pointer.stdout" ] ||
      [ "$(cat "$O/pointer.exit")" != 0 ] ||
      ! { rg -A2 'member=NotificationClosed' "$O/pointer.dbus" | rg -q 'uint32 2'; }; then
      echo "unexpected pointer outcome; inspect $O/pointer.dbus before updating the measured finding" >&2
      exit 1
    fi
  else
    [ "$(cat "$O/pointer.stdout")" = once ] || {
      echo "action control did not print once; verify input with $O/pointer.dbus before claiming body semantics" >&2; exit 1;
    }
  fi
  swaymsg -t get_seats >"$O/pointer-seats.json"
  {
    echo "native $MODE delivered by persistent zwlr_virtual_pointer_v1; absolute motion and button frames"
    echo "target=$((x+offset)),$((y+dy))"
    echo "stdout=$(cat "$O/pointer.stdout") exit=$(cat "$O/pointer.exit")"
    rg -A4 'member=NotificationClosed|member=ActionInvoked' "$O/pointer.dbus"
  } >"$O/pointer-result.txt"
  echo done >"$O/DONE"
  exit 0
fi

echo "== S1 send (a)"
mon_start s1_send
send s1
wait_visible s1_shown
echo "count=$(count)" >"$O/s1.count"
swaymsg -t get_seats >"$O/seats.json" 2>&1
sleep 1
echo "== S2 dismiss (b)"
if [ "$DAEMON" = swaync ]; then swaync-client --close-latest >"$O/s2.cmd" 2>&1; else makoctl dismiss >"$O/s2.cmd" 2>&1; fi
waitexit s1
mon_stop
wait_gone s2_after
echo "gone=$?" >"$O/s2.gone"

echo "== S3 invoke action 0 (c)"
mon_start s3_action
send s3
wait_visible s3_shown
if [ "$DAEMON" = swaync ]; then swaync-client --action 0 >"$O/s3.cmd" 2>&1; else makoctl invoke once >"$O/s3.cmd" 2>&1; fi
waitexit s3
mon_stop

echo "== S3b invoke action 2 (deny)"
mon_start s3b_action
send s3b
wait_visible s3b_shown
if [ "$DAEMON" = swaync ]; then swaync-client --action 2 >"$O/s3b.cmd" 2>&1; else makoctl invoke deny >"$O/s3b.cmd" 2>&1; fi
waitexit s3b
mon_stop

echo "== S4 pointer: body click (d)"
mon_start s4_bodyclick
send s4
wait_visible s4_shown
{
  wlrctl pointer move 2000 2000
  wlrctl pointer move -2000 -2000
  wlrctl pointer move 900 30
  wlrctl pointer click left
} >"$O/s4.cmd" 2>&1
sleep 2
shot s4_after
echo "count=$(count)" >"$O/s4.count"
mon_stop
if [ ! -s "$O/s4.exit" ]; then
  kill -INT "$(lastns)" 2>/dev/null
  waitexit s4
fi

echo "== S5 SIGINT (e)"
mon_start s5_sigint
send s5
wait_visible s5_shown
NSP=$(lastns)
echo "notify-send pid $NSP" >"$O/s5.cmd"
kill -INT "$NSP"
waitexit s5
wait_gone s5_after
echo "gone=$?" >"$O/s5.gone"
echo "count=$(count)" >>"$O/s5.gone"
mon_stop

echo "== S6 SIGKILL (e)"
mon_start s6_sigkill
send s6
wait_visible s6_shown
NSP=$(lastns)
echo "notify-send pid $NSP" >"$O/s6.cmd"
kill -KILL "$NSP"
sleep 3
shot s6_after
echo "mean=$(visible s6_after) count=$(count)" >"$O/s6.gone"
# the orphaned toast is still actionable: press "Allow once" on it (nobody listens)
if [ "$DAEMON" = swaync ]; then swaync-client --action 0 >"$O/s6.stale_action" 2>&1; else makoctl invoke once >"$O/s6.stale_action" 2>&1; fi
sleep 1
echo "after pressing the stale button: count=$(count)" >>"$O/s6.gone"
mon_stop
if [ "$DAEMON" = swaync ]; then
  swaync-client --close-latest >/dev/null 2>&1
  swaync-client -C >/dev/null 2>&1
else
  makoctl dismiss -a >/dev/null 2>&1
fi
sleep 1

echo "== S7 expiry critical, daemon timeout 3 (f)"
mon_start s7_expiry_critical
send s7
wait_visible s7_shown
sleep 6
shot s7_after6s
echo "mean=$(visible s7_after6s) count=$(count)" >"$O/s7.gone"
if [ "$DAEMON" = swaync ]; then
  swaync-client -op >/dev/null 2>&1
  sleep 1.5
  shot s7_center_open
  swaync-client -cp >/dev/null 2>&1
fi
mon_stop
kill -INT "$(lastns)" 2>/dev/null
waitexit s7
sleep 1

echo "== S7b expiry normal urgency (control)"
mon_start s7b_expiry_normal
(
  notify-send -u normal -a yolo -A once="Allow once" -A deny=Deny "normal toast" "body" \
    >"$O/s7b.stdout" 2>"$O/s7b.stderr"
  echo $? >"$O/s7b.exit"
) &
wait_visible s7b_shown
sleep 6
shot s7b_after6s
echo "mean=$(visible s7b_after6s) count=$(count)" >"$O/s7b.gone"
if [ "$DAEMON" = swaync ]; then
  swaync-client -op >/dev/null 2>&1
  sleep 1.5
  shot s7b_center_open
  swaync-client -cp >/dev/null 2>&1
fi
mon_stop
kill -INT "$(lastns)" 2>/dev/null
waitexit s7b
sleep 1

if [ "$DAEMON" = swaync ]; then
  echo "== S8 DND (g)"
  mon_start s8_dnd
  { echo "-dn prints: $(swaync-client -dn)"; echo "-D prints: $(swaync-client -D)"; } >"$O/s8.dnd_on" 2>&1
  # control: a normal-urgency toast while DND is on
  (
    notify-send -u normal -a yolo -A once="Allow once" -A deny=Deny "normal toast under DND" "body" \
      >"$O/s8n.stdout" 2>"$O/s8n.stderr"
    echo $? >"$O/s8n.exit"
  ) &
  sleep 2
  shot s8n_toast
  echo "normal under DND: toast mean=$(visible s8n_toast) count=$(count)" >"$O/s8.state"
  kill -INT "$(lastns)" 2>/dev/null
  waitexit s8n
  send s8
  sleep 4
  shot s8_toast
  echo "critical under DND: toast mean=$(visible s8_toast) count=$(count) dnd=$(swaync-client -D)" >>"$O/s8.state"
  swaync-client -op
  sleep 1.5
  shot s8_center_open
  swaync-client -cp
  swaync-client -df >/dev/null 2>&1
  mon_stop
  kill -INT "$(lastns)" 2>/dev/null
  waitexit s8
fi
echo "done" >"$O/DONE"
kill "$DPID" "$SWAYPID" 2>/dev/null
