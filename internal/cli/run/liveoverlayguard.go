package run

import (
	"path/filepath"
)

// refuseLiveWorkspaceLaunch reports whether this launch would build a jail on the
// LIVE workspace of the yolo jail it is itself running in — the accident
// AGENTS.md's "Nested-jail verification" rule exists for, promoted from prose to
// a refusal.
//
// The detection is structural, not heuristic: `YOLO_VERSION` is set in every yolo
// jail (it is how storage's migration detects in-jail, and how `just install`
// refuses there), and every container backend binds the workspace at exactly
// `/workspace` (assemble_parts.go's two base-mount builders). So INSIDE a jail,
// a workspace that resolves to `/workspace` is the running session's own live
// bind, and its per-workspace overlay — `<workspace>/.yolo/home` — IS that
// session's home. A fresh launch there regenerates agent config over a live
// session; measured twice: 2026-07-21, 479 Claude history entries eaten; and
// 2026-09-05, a cwd-drifted nested launch rewrote the session's
// yolo-user-env.sh from the wrong config and restaged its briefings. On the
// HOST (no YOLO_VERSION) nothing fires — a host launch on any workspace is the
// ordinary case, and a running jail for it is already handled by the attach
// path.
//
// The hatch (YOLO_ALLOW_LIVE_WORKSPACE=1) exists because a refusal this early
// cannot imagine every deliberate case; it is the YOLO_ALLOW_* pattern the
// source-skew and stale-image gates set.
func refuseLiveWorkspaceLaunch(o *Options) bool {
	if o.Getenv("YOLO_ALLOW_LIVE_WORKSPACE") != "" {
		return false
	}
	if o.Getenv("YOLO_VERSION") == "" {
		return false // on the host: no live session to clobber from here
	}
	return resolvePath(o.Workspace) == filepath.Clean("/workspace")
}
