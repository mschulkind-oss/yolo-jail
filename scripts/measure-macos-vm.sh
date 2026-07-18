#!/usr/bin/env bash
# measure-macos-vm.sh — Option A measurement for docs/design/macos-no-vm-direction.md
#
# Answers the three questions that gate the macOS "no-VM" decision, on real
# hardware (they can't be measured from a Linux dev jail):
#
#   1. STARTUP  — cold vs warm Apple Container start time.
#   2. RAM COMMIT — does AC pre-commit/steal the `--memory` ceiling up front,
#                   or is it a demand-paged cap?
#   3. RAM RELEASE — is that RAM returned to the host when the container exits?
#
# It measures Apple Container (`container`) and, if present, Podman Machine as
# a reference point (Podman Machine is the fixed-RAM VM we're comparing against).
#
# SAFE: only starts/stops throwaway `alpine` containers named `vmmeasure-*`
# and reads host memory stats. Cleans up after itself. No sudo. No yolo state.
#
# Usage:  bash scripts/measure-macos-vm.sh [--mem 8]   # --mem = ceiling in GiB
# Paste the final "RESULTS" block into docs/design/macos-no-vm-direction.md.

set -uo pipefail

MEM_GIB="${2:-8}"
[ "${1:-}" = "--mem" ] && MEM_GIB="${2:-8}"
IMAGE="alpine:latest"
IDLE_SECS=8   # how long to hold an idle container while sampling RAM

say() { printf '%s\n' "$*"; }
hr() { printf -- '----------------------------------------------------------\n'; }

# --- host memory helpers (macOS) -------------------------------------------
# "free-ish" = free + inactive + speculative pages (what the OS can hand out).
page_size() { vm_stat | awk -F'[ .]+' '/page size of/ {print $8; exit}'; }
free_mib() {
  local ps; ps=$(page_size); [ -z "$ps" ] && ps=16384
  vm_stat | awk -v ps="$ps" '
    /Pages free/          {f=$3}
    /Pages inactive/      {i=$3}
    /Pages speculative/   {s=$3}
    END { gsub(/\./,"",f); gsub(/\./,"",i); gsub(/\./,"",s);
          printf "%d", (f+i+s)*ps/1048576 }'
}
mem_pressure_pct() {
  # % of memory "free" per the kernel's own pressure accounting, if available.
  memory_pressure 2>/dev/null | awk -F': *' '/percentage free/ {gsub(/%/,"",$2); print $2; exit}'
}

now_ms() { python3 -c 'import time;print(int(time.time()*1000))' 2>/dev/null || perl -MTime::HiRes=time -e 'print int(time()*1000)'; }

# --- preflight --------------------------------------------------------------
if [ "$(uname -s)" != "Darwin" ]; then
  say "This script measures macOS virtualization — run it on the Mac, not in the jail."
  exit 2
fi
HOST_MEM_GIB=$(( $(sysctl -n hw.memsize) / 1073741824 ))
say "Host: $(sysctl -n hw.model 2>/dev/null || echo mac), ${HOST_MEM_GIB} GiB RAM, macOS $(sw_vers -productVersion)"
say "Ceiling under test: --memory ${MEM_GIB}g   (host has ${HOST_MEM_GIB} GiB)"
hr

HAVE_CONTAINER=0; command -v container >/dev/null 2>&1 && HAVE_CONTAINER=1
HAVE_PODMAN=0;    command -v podman    >/dev/null 2>&1 && HAVE_PODMAN=1

cleanup() {
  [ "$HAVE_CONTAINER" = 1 ] && container rm --force vmmeasure-ac >/dev/null 2>&1
  [ "$HAVE_PODMAN"    = 1 ] && podman   rm --force vmmeasure-pod >/dev/null 2>&1
}
trap cleanup EXIT

# ===========================================================================
# APPLE CONTAINER
# ===========================================================================
AC_COLD_MS="n/a"; AC_WARM_MS="n/a"; AC_BASE=""; AC_RUN=""; AC_AFTER=""
if [ "$HAVE_CONTAINER" = 1 ]; then
  say "APPLE CONTAINER ($(container --version 2>/dev/null | head -1))"
  container system start >/dev/null 2>&1
  container image pull "$IMAGE" >/dev/null 2>&1   # ensure image is local (don't time the pull)

  # 1. STARTUP — cold (system just started) is hard to force without a reboot;
  #    we time two consecutive `run`s: first is "cool", second "warm".
  t0=$(now_ms); container run --rm "$IMAGE" true >/dev/null 2>&1; t1=$(now_ms)
  AC_COLD_MS=$(( t1 - t0 ))
  t0=$(now_ms); container run --rm "$IMAGE" true >/dev/null 2>&1; t1=$(now_ms)
  AC_WARM_MS=$(( t1 - t0 ))
  say "  start (first run):  ${AC_COLD_MS} ms"
  say "  start (warm run):   ${AC_WARM_MS} ms"

  # 2/3. RAM COMMIT + RELEASE — sample host free RAM: baseline, while an idle
  #      container with a big --memory ceiling runs, and after it exits.
  AC_BASE=$(free_mib)
  container run -d --rm --name vmmeasure-ac --memory "${MEM_GIB}g" "$IMAGE" sleep "$((IDLE_SECS+4))" >/dev/null 2>&1
  sleep "$IDLE_SECS"
  AC_RUN=$(free_mib)
  container rm --force vmmeasure-ac >/dev/null 2>&1
  sleep 3
  AC_AFTER=$(free_mib)
  say "  host free MiB — baseline:${AC_BASE}  during(idle,${MEM_GIB}g cap):${AC_RUN}  after-exit:${AC_AFTER}"
  say "  → committed while idle: $(( AC_BASE - AC_RUN )) MiB (vs ${MEM_GIB}g=$(( MEM_GIB*1024 )) MiB ceiling)"
  say "  → returned on exit:     $(( AC_AFTER - AC_RUN )) MiB"
  hr
else
  say "APPLE CONTAINER: not installed — skipping"; hr
fi

# ===========================================================================
# PODMAN MACHINE (reference: the fixed-RAM VM we're comparing against)
# ===========================================================================
POD_WARM_MS="n/a"; POD_BASE=""; POD_RUN=""; POD_AFTER=""
if [ "$HAVE_PODMAN" = 1 ] && podman info >/dev/null 2>&1; then
  say "PODMAN MACHINE (reference)"
  POD_VM_MEM=$(podman machine inspect 2>/dev/null | awk -F': *' '/"Memory"/{gsub(/[",]/,"",$2);print $2;exit}')
  say "  machine RAM reserved: ${POD_VM_MEM:-?} MiB (this is pre-committed by the VM itself)"
  podman pull "$IMAGE" >/dev/null 2>&1
  t0=$(now_ms); podman run --rm "$IMAGE" true >/dev/null 2>&1; t1=$(now_ms)
  POD_WARM_MS=$(( t1 - t0 ))
  say "  start (warm run):   ${POD_WARM_MS} ms"
  POD_BASE=$(free_mib)
  podman run -d --rm --name vmmeasure-pod "$IMAGE" sleep "$((IDLE_SECS+4))" >/dev/null 2>&1
  sleep "$IDLE_SECS"; POD_RUN=$(free_mib)
  podman rm --force vmmeasure-pod >/dev/null 2>&1; sleep 3; POD_AFTER=$(free_mib)
  say "  host free MiB — baseline:${POD_BASE}  during:${POD_RUN}  after:${POD_AFTER}"
  hr
else
  say "PODMAN MACHINE: not running — skipping reference"; hr
fi

# ===========================================================================
# RESULTS — paste this block into docs/design/macos-no-vm-direction.md
# ===========================================================================
say "RESULTS (paste into docs/design/macos-no-vm-direction.md → ## Option A measurements)"
say ""
say "Host: ${HOST_MEM_GIB} GiB, macOS $(sw_vers -productVersion), ceiling ${MEM_GIB}g"
say ""
say "Apple Container:"
say "  startup:  first=${AC_COLD_MS}ms  warm=${AC_WARM_MS}ms"
say "  RAM idle-commit: $(( ${AC_BASE:-0} - ${AC_RUN:-0} )) MiB of ${MEM_GIB}g ceiling"
say "  RAM released on exit: $(( ${AC_AFTER:-0} - ${AC_RUN:-0} )) MiB"
say "Podman Machine (ref): warm=${POD_WARM_MS}ms, VM reserves ${POD_VM_MEM:-?} MiB up front"
say ""
say "INTERPRETATION:"
say "  - If AC idle-commit << ceiling (e.g. a few hundred MiB, not ${MEM_GIB}g),"
say "    then --memory is a demand-paged CAP, not a steal → the RAM-guess/steal"
say "    pain is largely a Podman-Machine problem, and AC is 'good enough' (Option C)."
say "  - If AC idle-commit ≈ ceiling and it's NOT released on exit, AC has the"
say "    same steal problem → the no-VM goal stands, pursue Option B."
say "  - Startup: warm << ~1s means the warm-VM path is acceptable; multi-second"
say "    cold start is the amortize-with-a-kept-alive-VM question."
