package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// brokenprefix.go turns ONE runtime error into a diagnosis: a jail whose yolo
// binaries were deleted out from under it while it was running.
//
// THE FAILURE. Since 04481a95 the jail does not contain yolo — /opt/yolo-jail/bin
// is a :ro bind mount the launch supplies (jailprefix.go), and the container argv
// names JailEntrypointPath absolutely. That mount is to a host DIRECTORY, and a
// bind mount pins an inode, not a path. Anything that empties or replaces that
// directory on the host leaves the running container mounted on a directory with
// nothing in it, and every subsequent `yolo` into that jail dies at exec:
//
//	Error: runc: exec failed: unable to start container process: exec:
//	"/opt/yolo-jail/bin/yolo-entrypoint": stat /opt/yolo-jail/bin/yolo-entrypoint:
//	no such file or directory
//
// Naming the path absolutely was deliberate and it is why that message says which
// file is missing at all (jailprefix.go, JailEntrypointPath). But it is the
// RUNTIME's sentence, and it describes a missing file rather than the thing that
// happened, which is that the jail is unrepairable and needs restarting. Two
// things do this in practice: a `just install` restaging the flake bundle (the
// staging script leads with `rm -rf $DEST`), and a store GC taking an unrooted
// install-prefix output (what OQ-BF4's roots and d6b2fc82's liveness guard exist
// to prevent, and what is still possible for anything they cannot see).
//
// WHY THIS IS A POST-MORTEM AND NOT A PRE-FLIGHT. The only authority on whether
// the container's mount still has contents is the container, and asking it costs
// an extra `exec` on EVERY attach — the hot path, in front of every session the
// user starts. A host-side stat is free but wrong in the common case: `just
// install` recreates the directory at the same path with a NEW inode, so the host
// sees a perfectly good bundle while the container sees an empty one. So this runs
// only after an exec has already failed, where an extra inspect costs nothing and
// the evidence is complete.

// prefixExecFailureCodes are the exit codes that mean "the runtime could not
// invoke the command", as opposed to "the command ran and exited non-zero".
// 127 is the observed one (podman's own exit for a container command that does
// not exist); 126 is its not-executable twin, and 125 is podman's code for its
// own failures, which covers a runtime that reports this differently.
//
// A false positive here costs one extra inspect and, if that inspect happens to
// find a genuinely broken mount, an accurate message. The set can therefore be
// generous: it is a filter on when to LOOK, not the diagnosis itself.
var prefixExecFailureCodes = map[int]bool{125: true, 126: true, 127: true}

// diagnoseBrokenPrefix explains an attach that died at exec, or returns "" when
// this is not that failure.
//
// It says nothing unless it can prove the shape: the exec failed with an
// invoke-failure code, the container really does mount its prefix bin/ from the
// host (a jail older than the mounted prefix does not, and answers ("", false)),
// and that host directory does not currently hold a yolo-entrypoint the container
// could be running. The last clause is what separates the two sentences it can
// produce — see brokenPrefixMessage.
func (o *Options) diagnoseBrokenPrefix(rt, cname string, rc int) string {
	if !prefixExecFailureCodes[rc] || o.Exec == nil {
		return ""
	}
	src, ok := prune.InspectPrefixBinMount(rt, cname, o.pruneRunFunc())
	if !ok || src == "" {
		// Either the container is gone (it may have died with the exec) or this
		// jail predates the mounted prefix. Neither is this failure, and guessing
		// at one would put a confident explanation on top of an unrelated error.
		return ""
	}
	return brokenPrefixMessage(src, o.pathExists(filepath.Join(src, "yolo-entrypoint")))
}

// brokenPrefixMessage is the pure text, split out so the two host verdicts are
// testable without a runtime.
//
// hostHasEntrypoint distinguishes REPLACED from DELETED, and the distinction is
// worth making because it names a different culprit. The container is running and
// its exec just failed on the mounted path, so the mounted directory is empty —
// that much is settled by the failure itself. If a good yolo-entrypoint sits at
// the same host path right now, then that path was recreated rather than merely
// removed, which is `just install`'s `rm -rf` + restage and nothing else. If it is
// absent, the directory was taken away and not put back — a store GC of an
// install-prefix output is the case that does that.
func brokenPrefixMessage(src string, hostHasEntrypoint bool) string {
	var b strings.Builder
	b.WriteString("[bold red]Cannot attach: this jail's yolo binaries were deleted " +
		"out from under it.[/bold red]\n")
	fmt.Fprintf(&b, "[dim]The jail runs pid1 from %s, a read-only bind mount of[/dim]\n",
		JailPrefixBinDir)
	fmt.Fprintf(&b, "[dim]  %s[/dim]\n", src)
	if hostHasEntrypoint {
		b.WriteString("[dim]on the host. That path holds a yolo-entrypoint now, so it was " +
			"REPLACED rather than\nremoved — a `just install` restages the flake bundle with " +
			"`rm -rf`, and a bind mount\nfollows the old inode, not the path.[/dim]\n")
	} else {
		b.WriteString("[dim]on the host. Nothing is there now: the directory was removed " +
			"while the jail was\nrunning (a store GC of an unrooted install prefix does " +
			"this).[/dim]\n")
	}
	b.WriteString("\n[dim]A running container's mounts cannot be repaired from outside it. " +
		"Restart the jail:[/dim]\n" +
		"[dim]  yolo stop      # from this workspace, finishing its running sessions[/dim]\n" +
		"[dim]  <rerun the same yolo command>[/dim]\n" +
		"[dim]Any session still live inside the jail keeps working; only new entries fail.[/dim]")
	return b.String()
}

// pathExists is the PathExists seam with its nil default applied. Every other
// caller reaches o.PathExists after Run has normalised it (runcmd.go); this one
// can be reached from a test that constructs Options directly, and a nil call
// there would panic in the middle of explaining someone else's failure.
func (o *Options) pathExists(p string) bool {
	if o.PathExists != nil {
		return o.PathExists(p)
	}
	_, err := os.Stat(p)
	return err == nil
}

// pruneRunFunc adapts the Exec seam to internal/prune's RunFunc. Three
// housekeeping passes each built this inline; the attach post-mortem is the
// fourth caller and the point at which one spelling is worth having.
//
// Ran is `res.Ran && !res.Timeout` for a reason worth keeping in one place: a
// timed-out probe still reports Ran=true with a zero-value RC (runcmd.go's
// realExec), so prune's `res.Ran && res.RC == 0` checks would read a killed
// process as a clean, empty success — "no containers", "no roots", answers that
// mean deletion to the callers asking.
func (o *Options) pruneRunFunc() prune.RunFunc {
	return func(argv []string, timeout time.Duration) prune.ProbeResult {
		res := o.Exec(argv, "", nil, timeout)
		return prune.ProbeResult{Stdout: res.Stdout, RC: res.RC, Ran: res.Ran && !res.Timeout}
	}
}
