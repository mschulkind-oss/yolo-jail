package runtime

// detachkeys.go turns off the runtime's DETACH SEQUENCE on every client yolo attaches to a jail
// (docs/design/jail-lifetime-last-session-wins.md §2.3 item 2, JL-D27).
//
// podman's `run -it` and `exec -it` clients watch the keys typed at them for a sequence that
// detaches the client from what it started, `ctrl-p,ctrl-q` unless a host's containers.conf
// says otherwise. Typing it in a session left the process that session started running with no
// terminal, and the launcher, which had just seen its client return, went on as if the session
// had ended: an agent running headless, counted by nobody. An empty value turns the feature off
// whatever the host's setting, which is what podman's help documents the flag for.
//
// MEASURED 2026-09-30 on podman 5.8.7 (a nested rootful podman in a jail, under a pty): with the
// default, `ctrl-p` then `ctrl-q` typed as separate keystrokes made both `exec -it` and
// `run -it` clients exit 0 with their process still running in the container; with
// `--detach-keys=` the same keystrokes reached the process. podman recognizes the sequence only
// when each key arrives in a read of its own, which is how a person types it. Both flags are in
// `podman run --help` and `podman exec --help`, the remote client's included, and podman accepts
// the flag on a detached run and on an exec with no terminal.
//
// podman only. Apple Container's `container run` and `container exec` get nothing until a probe
// or their help shows a flag or a detach sequence there: podman's flag, passed unmeasured, could
// fail every launch and attach on an unknown flag (JL-D27).

// DetachKeysOff is podman's flag with an empty sequence, which disables detaching.
const DetachKeysOff = "--detach-keys="

// DetachKeysArgs is what every `run` and `exec` yolo issues into a jail on rt carries to disable
// the detach sequence: DetachKeysOff on podman, nothing on any other runtime.
func DetachKeysArgs(rt string) []string {
	if rt != "podman" { // parity: NotApplicable — Apple Container's detach behavior is unmeasured (JL-D27)
		return nil
	}
	return []string{DetachKeysOff}
}
