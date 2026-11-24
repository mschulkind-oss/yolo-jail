#!/usr/bin/env python3
"""Tests for scripts/mac-ac-linux-builder.sh's start step, against a fake `container` CLI.

The Apple Container parity job failed three times on 2026-11-24 with only
"could not start yolo-ac-builder": the script threw the runtime's own error away, and a
stopped builder left behind when the runtime was booted out kept the name taken while
`container ls` (running only) did not show it.
"""
import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parent / "mac-ac-linux-builder.sh"

# A fake `container`: system status answers; image ls lists the builder image; ls without
# --all lists running containers from $FAKE/running; ls --all also lists $FAKE/stopped;
# run fails naming the conflict while a stopped one exists (or always, with $FAKE/runfails);
# rm removes the stopped one. Every call is logged.
FAKE_CONTAINER = r"""#!/bin/sh
echo "$*" >> "$FAKE/calls"
case "$1" in
system) exit 0 ;;
image) echo "ghcr.io/mschulkind-oss/yolo-jail-builder latest"; exit 0 ;;
ls)
    [ -f "$FAKE/running" ] && cat "$FAKE/running"
    [ "$2" = --all ] && [ -f "$FAKE/stopped" ] && cat "$FAKE/stopped"
    exit 0 ;;
rm) rm -f "$FAKE/stopped"; exit 0 ;;
run)
    if [ -f "$FAKE/runfails" ]; then echo "Error: internalError: \"VM failed to boot\"" >&2; exit 1; fi
    if [ -f "$FAKE/stopped" ]; then echo "Error: exists: \"container with id yolo-ac-builder already exists\"" >&2; exit 1; fi
    echo "yolo-ac-builder img linux arm64 running 192.168.64.9/24" > "$FAKE/running"
    exit 0 ;;
*) exit 0 ;;
esac
"""


class StartStep(unittest.TestCase):
    def run_script(self, fake: pathlib.Path) -> subprocess.CompletedProcess:
        bindir = fake / "bin"
        bindir.mkdir()
        c = bindir / "container"
        c.write_text(FAKE_CONTAINER)
        c.chmod(0o755)
        keydir = fake / "keys"
        keydir.mkdir(mode=0o700)
        (keydir / "builder_key").write_text("key\n")
        (keydir / "builder_key.pub").write_text("ssh-ed25519 AAAA test\n")
        # The stages after the start step read the builder's host key: answer at once, so
        # the script runs to its spec line instead of waiting out its keyscan retries.
        scan = bindir / "ssh-keyscan"
        scan.write_text("#!/bin/sh\necho \"$3 ssh-ed25519 AAAAhostkey\"\n")
        scan.chmod(0o755)
        env = dict(os.environ, FAKE=str(fake), PATH=f"{bindir}:{os.environ['PATH']}",
                   YOLO_AC_BUILDER_KEYDIR=str(keydir))
        return subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True,
                              timeout=120)

    def test_a_stopped_leftover_builder_is_removed_and_the_start_retried(self):
        with tempfile.TemporaryDirectory() as d:
            fake = pathlib.Path(d)
            (fake / "stopped").write_text("yolo-ac-builder img linux arm64 stopped\n")
            r = self.run_script(fake)
            calls = (fake / "calls").read_text()
            self.assertIn("rm yolo-ac-builder", calls, r.stderr)
            self.assertNotIn("could not start", r.stderr)
            self.assertIn("builder at 192.168.64.9", r.stderr)
            self.assertEqual(r.returncode, 0, r.stderr)

    def test_a_failed_start_prints_the_runtimes_own_error(self):
        with tempfile.TemporaryDirectory() as d:
            fake = pathlib.Path(d)
            (fake / "runfails").write_text("")
            r = self.run_script(fake)
            self.assertNotEqual(r.returncode, 0)
            self.assertIn("could not start yolo-ac-builder", r.stderr)
            self.assertIn("VM failed to boot", r.stderr, "the runtime's error must reach the log")
            self.assertIn("container logs yolo-ac-builder", r.stderr, "a stop names the next step")


if __name__ == "__main__":
    unittest.main()
