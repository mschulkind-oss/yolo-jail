#!/usr/bin/env bash
# Run vantage-check, at its latest release, on a pipe of its own. `just lint-ci` calls this, never
# `uvx vantage-check` directly.
#
# vantage-check leaves its stdout and stderr in non-blocking mode (O_NONBLOCK) when it
# exits. That flag belongs to the open pipe, not to the process, so every other process
# writing to the same pipe gets it too. `just check-ci` runs lint-ci and test-fast in
# parallel on one shared output pipe, so once vantage-check had run, `go test`'s next large
# write could be cut short with EAGAIN. That is the write printing a failing package's
# output, so CI's log lost the `--- FAIL` lines and showed only a bare `FAIL`.
#
# Here vantage-check writes into a fresh pipe that only `cat` reads, so the flag lands on
# that pipe, and the gate's shared output stays blocking. pipefail keeps vantage-check's
# own exit status.
set -euo pipefail
# UNPINNED, by the owner's ruling (2026-11-10, CAC-D16): @latest makes uvx check for a new
# release on every run, so the gate always runs vantage-check's current rules.
uvx vantage-check@latest "$@" </dev/null 2>&1 | cat
