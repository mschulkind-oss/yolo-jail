package run

// backendcaps.go holds the predicates that answer "can this backend do X?" for the
// run pipeline — the thing docs/design/backend-parity.md is about.
//
// It exists because of a defect shape rather than a tidiness urge. The `:ro` rule
// below spent its whole life as `ctxMountsUnsafe := rt == "container"`, a local
// variable inside assemble.go's config-`mounts` loop. That is a perfectly good place
// to put a rule you believe has one call site, and a bad place to put one that turns
// out to have two: when the pack `mount` kind landed — emitting the identical
// `-v host:dest:ro` argv from packhostgrants.go — there was no shared thing for it to
// consult, so it silently didn't. A rule reachable only from the function that
// discovered it will be re-discovered, or not.
//
// The bar for adding a predicate here: a capability at least two call sites must agree
// on, stated once, with its evidence in the comment. A single-site `rt ==` check is
// still fine at its site.

// roBindsUnsupported reports why a backend cannot honor a read-only bind mount,
// or "" when it can.
//
// Apple Container accepts `-v src:dest:ro` and IGNORES the suffix, which is the
// dangerous failure mode rather than the annoying one: the mount succeeds, so nothing
// looks wrong, and the agent holds write access to a host directory the user granted
// as read-only. Both callers therefore refuse the mount rather than downgrade it —
// there is no read-only bind to fall back to, and handing over a writable one on a
// backend the user picked for isolation is not a degradation anyone consented to.
//
// macos-user is deliberately absent: it has no bind mounts at all, so a `:ro` question
// does not arise there. Its gaps are reported by noteMacosUserContentGaps.
func roBindsUnsupported(rt string) string {
	if rt == "container" {
		return "Apple Container ignores read-only (:ro), so it would be writable. " +
			"Use `YOLO_RUNTIME=podman` for read-only context mounts."
	}
	return ""
}
