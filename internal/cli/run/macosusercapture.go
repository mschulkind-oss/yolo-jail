package run

// macosusercapture.go is E3 on the macos-user arm: the capture-on-terminate fold a container's
// teardown runs (captureConfigOnTerminate), run once this backend's session has returned.
//
// E3 is the name docs/plans/setup-support-gaps.md gives that fold: a session's in-jail edits to
// capture-mode surfaces (an agent's own settings file, say) are folded into their overlay
// sidecars from the host side when the session ends, so `yolo config diff` answers about the
// session that just ended rather than the one before it. The next boot render folds the same
// edits, so skipping it costs that answer and never data.

// captureMacosUserConfig runs the E3 fold for the macos-user session that has just returned.
//
// UNDER THE WORKSPACE LAUNCH LOCK, TAKEN WITHOUT WAITING. This backend runs several sessions of
// one workspace at once, each with its own bootstrap, and a bootstrap's boot render writes the
// surfaces, baselines and overlay sidecars this fold reads and writes. A fold that read a freshly
// rendered surface against the baseline that render was replacing would record yolo's own change
// as the user's. The backend's bootstrap runs inside this lock (macosuser.RunMacosUser takes it
// through AcquireWorkspaceLockFor), so a lock held elsewhere means another launch of the workspace
// is mid-setup, and its boot render folds the same edits: skipping loses nothing.
//
// This launch's own hold was handed to the backend, which releases it before its agent starts;
// the release here covers a backend that returned before reaching that point, and is a no-op
// otherwise.
func (o *Options) captureMacosUserConfig(cname, rt string) {
	o.releaseLaunchLock()
	lock, ok := tryWorkspaceLock(cname)
	if !ok {
		return
	}
	defer lock.Close()
	sp := o.Perf.Span("shutdown.capture_config")
	defer sp.End()
	o.captureConfigOnTerminate(rt)
}
