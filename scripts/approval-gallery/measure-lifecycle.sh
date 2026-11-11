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
[ "$#" -ge 3 ] && [ "$#" -le 6 ] || { echo "usage: $0 <swaync|mako> <out-dir> <short run-dir> [--pointer-only|--pointer-action|--candidate <stdin hook> <pending fixture.json>]" >&2; exit 2; }
case $1 in swaync|mako) ;; *) echo "use swaync or mako" >&2; exit 2 ;; esac
MODE=${4:-all}
case $MODE in all|--pointer-only|--pointer-action|--candidate) ;; *) echo "unknown mode: $MODE; use --pointer-only, --pointer-action or --candidate" >&2; exit 2 ;; esac
HERE=$(cd "$(dirname "$0")" && pwd)
HOOK="" FIXTURE=""
if [ "$MODE" = --candidate ]; then
  # The candidate reads a schema-1 request-filed event on stdin and answers only through
  # the absolute YOLO_BIN, here a stub that logs its arguments and decides nothing.
  [ "$#" = 6 ] && [ -x "$5" ] && [ -f "$6" ] || { echo "--candidate needs an executable stdin hook and a fixture file: $0 $1 <out-dir> <run-dir> --candidate <hook> <fixture.json>" >&2; exit 2; }
  HOOK=$(realpath "$5") FIXTURE=$(realpath "$6")
  "$HERE/request-event.sh" "$FIXTURE" >/dev/null || { echo "--candidate needs a pending fixture; choose one request-event.sh accepts" >&2; exit 2; }
elif [ "$#" -gt 4 ]; then
  echo "only --candidate takes further arguments" >&2; exit 2
fi
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
# closed_reason prints the REASON of the first NotificationClosed in a monitor log: the
# second argument, never the id line before it (an id of 2 is not a dismissal).
closed_reason() { awk '/member=NotificationClosed/ && !n {n=NR} n && NR==n+2 {print $2; exit}' "$1"; }


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

if [ "$MODE" = --candidate ]; then
  mkdir -p "$O/stub" "$O/home"
  STUBLOG=$O/stub-yolo.log
  printf '#!/bin/sh\necho "yolo $*" >>"%s"\n' "$STUBLOG" >"$O/stub/yolo"
  chmod +x "$O/stub/yolo"
  RID=$(jq -r .request.id "$FIXTURE")
  R=$O/candidate-result.txt
  FAILS=0
  : >"$R"
  : >"$O/stub-all.log"
  check() { # what, want, got
    if [ "$2" = "$3" ]; then echo "PASS $1: [$3]" >>"$R"; else echo "FAIL $1: want [$2] got [$3]" >>"$R"; FAILS=$((FAILS + 1)); fi
  }
  crun() { # name, event file: the hook as yolo would start it, with nothing else in its environment
    (
      env -i HOME="$O/home" PATH="$PATH" DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" \
        YOLO_BIN="$O/stub/yolo" "$HOOK" <"$2" >"$O/$1.stdout" 2>"$O/$1.stderr"
      echo $? >"$O/$1.exit"
    ) &
    SENDPID=$!
  }
  chook() { pgrep -n -P "$SENDPID"; }
  sig() { kill "-$1" "$(chook)"; }
  await_exit() { timeout 120 bash -c 'until [ -s "$1" ]; do sleep 0.1; done' _ "$O/$1.exit"; }
  bodyclick() { # a real pointer press in the toast's body whitespace, right of the text
    box=$(magick "$O/$1_shown.png" -fuzz 1% -format '%@' info:)
    w=${box%%x*}
    h=$(echo "$box" | sed -E 's/^[0-9]+x([0-9]+)\+.*/\1/')
    x=$(echo "$box" | sed -E 's/.*\+([0-9]+)\+([0-9]+)$/\1/')
    y=$(echo "$box" | sed -E 's/.*\+([0-9]+)\+([0-9]+)$/\2/')
    echo "toast bounding box=$box target=$((x + w - 30)),$((y + h / 2))" >"$O/pointer.cmd"
    pointer_client "$((x + w - 30))" "$((y + h / 2))" >>"$O/pointer.cmd" 2>&1
  }
  cstep() { # name, the one stub line expected ("" for none), seconds to expiry, trigger...
    n=$1 want=$2 ttl=$3
    shift 3
    : >"$STUBLOG"
    rm -f "$O/$n.exit"
    "$HERE/request-event.sh" "$FIXTURE" "$ttl" >"$O/$n.event.json" || return 1
    mon_start "$n" || return 1
    crun "$n" "$O/$n.event.json"
    prev=""
    if ! wait_visible "${n}_shown"; then
      check "$n: toast shown" yes no
      kill -INT "$(chook)" 2>/dev/null
      mon_stop
      return 1
    fi
    "$@" >"$O/$n.cmd" 2>&1
    # Every candidate path ends by closing its own id, then exiting: wait on both events.
    await_exit "$n" || check "$n: hook exited" yes no
    await_log 'member=CloseNotification' "$O/$n.dbus" || check "$n: CloseNotification sent" yes no
    wait_gone "${n}_after"
    gone=$?
    kill "${POINTERPID:-}" 2>/dev/null
    POINTERPID=""
    gdbus call --session -d org.freedesktop.Notifications -o /org/freedesktop/Notifications \
      -m org.freedesktop.Notifications.GetServerInformation >/dev/null 2>&1
    mon_stop
    cp "$STUBLOG" "$O/$n.stub"
    cat "$STUBLOG" >>"$O/stub-all.log"
    check "$n: stub yolo calls" "$want" "$(cat "$O/$n.stub")"
    check "$n: stub yolo call count" "$([ -n "$want" ] && echo 1 || echo 0)" "$(wc -l <"$O/$n.stub" | tr -d ' ')"
    check "$n: hook exit" 0 "$(cat "$O/$n.exit" 2>/dev/null)"
    check "$n: hook stdout is empty" "" "$(cat "$O/$n.stdout")"
    check "$n: toast withdrawn" 0 "$gone"
  }
  action() { if [ "$DAEMON" = swaync ]; then swaync-client --action "$1"; else makoctl invoke "$2"; fi; }
  dismiss() { if [ "$DAEMON" = swaync ]; then swaync-client --close-latest; else makoctl dismiss; fi; }
  invoked() { rg -c 'member=ActionInvoked' "$O/$1.dbus" || true; }
  [ "$DAEMON" != mako ] || pointer_client --build || exit 1

  echo "== candidate: Allow once"
  cstep c_once "yolo approve $RID" 900 action 0 once
  # What the candidate actually put on the bus, as the daemon received it.
  rg -A40 'member=Notify$' "$O/c_once.dbus" | sed -n '1,/^method return\|^signal\|^method call.*member=[^N]/p' >"$O/notify-call.txt"
  check "Notify: offered action keys" "once 15m deny" "$(rg -o 'string "(once|15m|deny|hour|default)"' "$O/notify-call.txt" | sed -E 's/string "(.*)"/\1/' | tr '\n' ' ' | sed 's/ $//')"
  check "Notify: default action entries" "" "$(rg -c 'string "default"' "$O/notify-call.txt" || true)"
  check "Notify: expire timeout" "int32 0" "$(rg -o 'int32 -?[0-9]+' "$O/notify-call.txt" | tail -1)"
  check "c_once: ActionInvoked signals" 1 "$(invoked c_once)"
  echo "== candidate: Allow for 15 minutes"
  cstep c_15m "yolo approve $RID --for 15m" 900 action 1 15m
  echo "== candidate: Deny button"
  cstep c_deny "yolo approve $RID --deny" 900 action 2 deny
  echo "== candidate: user dismissal (reason 2)"
  cstep c_dismiss "yolo approve $RID --deny" 900 dismiss
  check "c_dismiss: close reason" 2 "$(closed_reason "$O/c_dismiss.dbus")"
  check "c_dismiss: ActionInvoked signals" "" "$(invoked c_dismiss)"
  if [ "$DAEMON" = mako ]; then
    echo "== candidate: real pointer press on the body"
    cstep c_body "yolo approve $RID --deny" 900 bodyclick c_body
    check "c_body: close reason" 2 "$(closed_reason "$O/c_body.dbus")"
    check "c_body: ActionInvoked signals" "" "$(invoked c_body)"
  else
    echo "SKIP c_body: no working pointer control exists for swaync in this stack; its body click is unmeasured" >>"$R"
  fi
  echo "== candidate: SIGINT (how yolo stops a hook)"
  cstep c_sigint "" 900 sig INT
  check "c_sigint: ActionInvoked signals" "" "$(invoked c_sigint)"
  echo "== candidate: SIGTERM"
  cstep c_sigterm "" 900 sig TERM
  echo "== candidate: request expiry"
  cstep c_expiry "" 8 true
  check "c_expiry: ActionInvoked signals" "" "$(invoked c_expiry)"

  echo "== candidate: refused inputs post nothing"
  : >"$STUBLOG"
  jq -c '{schema:1,event:"request-filed",source:.request.source,time:.request.filed,summary:"",request:.request}' \
    "$FIXTURE" >"$O/c_stale.event.json" # the fixture's own, long-expired request, not re-timed
  : >"$O/c_empty.event.json"
  for n in c_stale c_empty; do
    rm -f "$O/$n.exit"
    mon_start "$n" || exit 1
    crun "$n" "$O/$n.event.json"
    await_exit "$n"
    gdbus call --session -d org.freedesktop.Notifications -o /org/freedesktop/Notifications \
      -m org.freedesktop.Notifications.GetServerInformation >/dev/null 2>&1
    mon_stop
    check "$n: hook exit" 1 "$(cat "$O/$n.exit")"
    check "$n: Notify calls" "" "$(rg -c 'member=Notify$' "$O/$n.dbus" || true)"
    check "$n: stub yolo calls" "" "$(cat "$STUBLOG")"
    echo "$n stderr: $(cat "$O/$n.stderr")" >>"$R"
  done
  {
    echo "stub yolo totals over the whole run (a logging-only stub: nothing was approved or denied):"
    sort "$O/stub-all.log" | uniq -c
  } >>"$R"
  echo "failures=$FAILS" >>"$R"
  echo done >"$O/DONE"
  [ "$FAILS" = 0 ] || { echo "candidate lifecycle: $FAILS failed; see $R" >&2; exit 1; }
  exit 0
fi

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
      [ "$(closed_reason "$O/pointer.dbus")" != 2 ]; then
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
