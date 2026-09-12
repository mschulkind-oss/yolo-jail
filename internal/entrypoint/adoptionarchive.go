package entrypoint

// adoptionarchive.go is OQ-CO7's ONE-TIME ADOPTION ARCHIVE
// (docs/design/config-ownership-and-promotion.md §6.3.3): before the first render that
// composes a surface file WHOLESALE out of what that file already holds, the pre-existing
// content is copied once, so a key the adoption drops is still somewhere.
//
// # Why it exists at all, given everything else that guards a host apply
//
// `confirmHostLosses` — the one-way-door prompt — is STRUCTURALLY BLIND to the loss this
// covers. It reads HostRenderResult.EntryLosses, which is defined over NAMED ENTRIES in a
// table (an mcpServers record), and it fires only on a FIRST APPLY. The transition that loses
// a deep-merged leaf is `host_management: assert` -> `own` on a home yolo has already applied
// to: not a first apply, and not an entry. So the prompt says "nothing would be lost" and the
// leaf goes. A keyless surface is the same shape one step further out — no tables, no entries,
// and a whole-file replacement — which is why OQ-CO9 refuses one outright at the host notch.
//
// # Why it is owed at BOTH notches, and why they cannot share a primitive (P5)
//
// The jail's boot render adopts through exactly the same branch (agentcfg.ComposeStateful's
// firstMigration arm), against a home that may hold an agent's own state — copilot's OAuth
// tokens are the shipped example of what that branch used to destroy. But a BOOT HAS NO TTY
// to prompt on: in-jail `yolo apply` is a report, not a provision. So the RULE is uniform and
// the NET splits by the primitive available — a prompt where there is a human, a copy where
// there is not. That is what makes the copy load-bearing rather than a nicety, and it is why
// this file is reached from the shared writer rather than from either notch's entry.
//
// # What it is NOT
//
// Not per-apply snapshots: those are a different feature with a retention policy. Not a
// history either — the later-regression case (a pack update deleting a key months from now) is
// what having the pack in git is for, which is the whole arrangement `own` recommends. ONE
// copy, of the file as yolo first found it, per surface, per home.

import (
	"fmt"
	"os"
	"path/filepath"
)

// archiveAdoption writes the one-time archive for an adopting render, records the path on r,
// and announces it. It returns an error only when a copy was OWED and could not be made.
//
// THE THREE CONDITIONS, in the order they are cheapest to answer:
//
//   - FirstMigration. The render is composing the whole file out of what the file already
//     holds (ComposeStateful's adoption branch). A steady-state render has a trusted baseline,
//     captures a diff against it, and loses nothing this could net — so it archives nothing,
//     which is what keeps "one archive" true without a sentinel of its own.
//   - Some bytes to archive. An ABSENT file is the overwhelmingly common case (every surface
//     of a fresh jail home) and there is nothing to lose; an EMPTY one is the same answer for
//     the same reason — a user restoring either gets the same result, and an archive of zero
//     bytes is not a net, it is a directory entry saying yolo ran.
//   - No archive already there. See below.
//
// ⚠ IT IS DELIBERATELY NOT GATED ON "WOULD THE RENDER CHANGE THE FILE?", which is the
// attractive fourth condition and the wrong one. That predicate is available here for free
// (r.text() versus r.current, the writer's own bytes) and it would mean §11's zero-bytes
// switch leaves no archive at all. But it makes the net conditional on the render being
// CORRECT — deploying only when adoption is believed to have lost nothing — and the archive
// exists precisely because adoption might be wrong about that. A net that trusts the thing it
// nets is not a net.
func archiveAdoption(e *Env, r *statefulRender) error {
	if r == nil || r.out == nil || !r.out.FirstMigration || len(r.current) == 0 {
		return nil
	}
	target := e.renderTarget()
	dest := target.ArchivePath(r.surface.Agent, r.surface.Name, filepath.Base(r.path))
	if dest == "" {
		// NOWHERE TO PUT ONE, which is a refusal rather than a skip. Unreachable through
		// either shipped notch — a jail target always has a workspace and a host target
		// always has a home — and it stays fail-closed for the next notch that reaches this
		// writer without having said where its archive lives (render.KindGuest is the one
		// waiting to). Silently adopting without a net is the outcome this whole file exists
		// to prevent, so it must not be the answer to a Target nobody finished.
		return refuseRMW(r.surface, "no adoption archive directory at this target, and "+
			"composing %s wholesale out of what it already holds is a one-way door — "+
			"refusing rather than adopting with no copy of the file (OQ-CO7)", r.path)
	}
	// IDEMPOTENCY IS THE ARCHIVE'S OWN EXISTENCE — no sentinel, no record to keep in sync.
	// A second adoption of the same surface is reachable (`yolo config reset`, a deleted or
	// corrupt last_render, a restored workspace), and re-archiving then would copy YOLO'S OWN
	// OUTPUT over the user's original. That is not a weaker version of the net; it is the
	// deletion the net exists to prevent, performed by the net. So the first copy wins and
	// every later adoption writes nothing.
	if _, err := os.Lstat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), target.SidecarDirMode()); err != nil {
		return archiveRefusal(r, dest, err)
	}
	// The bytes composeStatefulSurface already read, not a re-read of the path: a second read
	// is a second answer, and the file this copy is about is the one the render composed from.
	if err := WriteInPlace(dest, r.current, target.SidecarFileMode()); err != nil {
		return archiveRefusal(r, dest, err)
	}
	if err := os.Chmod(dest, target.SidecarFileMode()); err != nil {
		return archiveRefusal(r, dest, err)
	}
	r.archived = dest
	// DISCLOSURE, not a warning: adoption is a supported, deliberate act and the copy is the
	// good news. It is unsuppressible for OQ-RO3's reason — an archive the user cannot find is
	// the same as a deletion from where they stand — and it fires at most once per surface per
	// home, so it can afford to be a line. At the host notch e.Stderr is nil by design and
	// this says nothing; that report is HostRenderResult.Archived's, printed by `yolo host
	// apply` where the rest of the surface's story is.
	e.warn(fmt.Sprintf("%s: adopted; the file as yolo found it is archived at %s",
		r.path, dest))
	return nil
}

// archiveRefusal turns a failed archive write into a per-surface REFUSAL — the render does not
// happen and the file is left exactly as the agent wrote it.
//
// REFUSE RATHER THAN WARN-AND-PROCEED, argued from what the surrounding code does with
// comparable failures rather than from taste:
//
//   - persistStatefulSurface already returns an error for every other write in the adopting
//     path (the surface file, each capture sidecar). An archive that could not be written is
//     an I/O failure in the same write half, on the same tree, and usually the SAME failure —
//     a workspace that cannot take the archive cannot take the overlay either. Warning here
//     while erroring one line later would be two answers to one condition.
//   - The host dispatch's stateful arm already states the disposition for a refusal arriving
//     at the write: "a per-surface result, not a pack-level error … the file is untouched
//     either way", with the remaining surfaces still rendered. This is that, exactly — which
//     is why the carrier is the refusal type rather than a bare error.
//   - The alternative is a net that can silently not exist, which is worth less than one that
//     says so. Proceeding would take the one-way door WITHOUT the thing that makes it
//     survivable, and report it as a successful render.
//
// The cost of being wrong in this direction is bounded and self-healing: the surface keeps the
// content it already had, nothing is written, and the next render retries the whole adoption
// from the same pre-existing file — because no last_render was persisted either.
func archiveRefusal(r *statefulRender, dest string, err error) error {
	return refuseRMW(r.surface, "could not archive %s to %s before adopting it (%v) — "+
		"composing the whole file out of what it holds is a one-way door, and this copy is "+
		"the only record of what it held (OQ-CO7); the file is untouched", r.path, dest, err)
}
