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
  printf 'default-timeout=3000\n' >"$XDG_CONFIG_HOME/mako/config"
  mako >"$O/daemon.log" 2>&1 &
fi
DPID=$!
MONPID="" SENDPID=""
# An interrupted run leaves nothing behind: the monitor, the daemon, sway, and any notifier.
trap 'for p in $SENDPID; do pkill -P "$p" 2>/dev/null; done; kill $MONPID "$DPID" "$SWAYPID" 2>/dev/null' EXIT
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
      sleep 0.5
      shot "$1"
      return 0
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
mon_start() {
  dbus-monitor --session "type='method_call',interface='org.freedesktop.Notifications'" \
    "type='signal',interface='org.freedesktop.Notifications'" "type='method_return'" \
    "type='error'" >"$O/$1.dbus" 2>&1 &
  MONPID=$!
  sleep 1
}
mon_stop() {
  sleep 0.5
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
