package cli

// hostrevert.go is the `yolo host apply --revert` call site: withdraw yolo from the config
// files it has written into the user's real home, on the authority of the provenance record
// it wrote there (docs/design/config-ownership-and-promotion.md §10 step 3).
//
// The mechanism is entrypoint.RevertHostRender, which owns the record reading, the
// eligibility rules and the RMW write — the same walk PruneHostOverlayKeys makes, with a
// wider eligible-layer set. What lives HERE is the posture and the report.
//
// # The dry run IS the confirmation
//
// Every other host write in this command rides a [y/N]; this one does not, and the difference
// is which act the user performed. `confirmHostLosses` and the dropped-pack prompt guard
// losses that happen as a SIDE EFFECT of an apply the user ran for another reason — they
// asked to render their packs and are told what that costs. A revert is the user naming this
// operation exactly, and it is a DRY RUN by default: the first invocation lists every key it
// would remove with the attribution each removal rests on, and only a second one carrying
// --assert writes. A prompt on top of that would ask the same question twice, which is the
// prompt fatigue confirmHostLosses' own docstring refuses to build.
//
// # It RUNS under `none`, and is refused under `own` and the retired `"assert"`
//
// `none` is the unset state since the `assert` retirement (OQ-CO14), and a home `assert` wrote
// into keeps every key it last rendered: the ruling leaves the file exactly as it was, with no
// prompt and no notice. This verb is the one way back from there to a file purely the user's,
// so it runs under `none` on the authority of the provenance record that render left — which
// does not depend on the contract — and says "nothing to revert" on a home with no record. It
// used to refuse under `none` and name `"assert"` as the way to reach it, which once the value
// went would have left no value under which yolo's keys could come out.
//
// Under `own` it is still refused, and the refusal names `none`: the file is derived output, so
// a key-by-key retreat would be undone by the next apply composing the same keys back. The
// way out of an owned file is to stop declaring it — set `none`, then revert.

import (
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostRevertRefusal is the message for an ownership contract under which a revert is not the
// operation the user wants, or "" when it may proceed — which it may under `none`, set or
// unset (see the file comment).
//
// SEPARATE FROM hostManagementRefusal, rather than reusing it, because that one's sentences
// are about a render that will not happen and would be actively misleading here.
func hostRevertRefusal(mode config.HostManagement) string {
	switch mode {
	case config.HostManagementOwn:
		return "`host_management` is \"own\" in " + paths.UserConfigPath() + ", which says " +
			"these files are derived output, so the next apply would compose back every key a " +
			"revert took out.\n" +
			"  To withdraw yolo key by key, set the key to \"none\" (or delete it) and re-run " +
			"this; to keep a file owned and lose one key, take it out of the pack that declares it."
	}
	return ""
}

// refuseHostRevert stops a revert the declared contract does not permit. EXIT 1, not 2, for
// refuseHostManagement's reason: nothing about the argv is wrong.
func refuseHostRevert(errw io.Writer) (int, bool) {
	if rc, refused := retiredHostManagementRefusal(errw, "yolo host apply --revert"); refused {
		return rc, true
	}
	msg := hostRevertRefusal(config.HostManagementMode())
	if msg == "" {
		return 0, false
	}
	fmt.Fprintf(errw, "yolo host apply --revert: %s\n", msg)
	return 1, true
}

// hostRevert runs the revert and reports it. write=false is the observe posture.
func hostRevert(out, errw io.Writer, color bool, write bool) int {
	pr := richtext.Printer{W: out, Color: color}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply --revert: cannot resolve your home: %v\n", err)
		return 1
	}
	posture := fmt.Sprintf("dry run over %s; nothing is written", home)
	if write {
		posture = fmt.Sprintf("withdrawing from %s", home)
	}
	pr.Printf("[bold]host apply --revert[/bold] — %s", posture)

	result, rerr := entrypoint.RevertHostRender(hostRevertCandidates(errw), home, !write)
	// The keys found before the error are still reported: a revert that failed halfway has
	// done part of its work, and the user needs to know which part.
	for _, k := range result.Keys {
		pr.Printf("  [yellow]%-20s %s %s[/yellow] [dim](%s)  %s[/dim]",
			k.Surface, k.Action, k.Key, k.Layer, k.Path)
	}
	// The keys of yolo's the revert LEAVES, named for the reason the removals are: after it
	// the file still holds them, and a report listing only removals would read as if nothing
	// of yolo's remained. An empty default is the shape the pack declares its file needs
	// (entrypoint.keptShapeDefault), so taking it out would leave a file its agent rejects.
	for _, k := range result.Kept {
		pr.Printf("  [dim]%-20s keeps %s (%s: an empty default, the shape the pack declares "+
			"this file needs)  %s[/dim]", k.Surface, k.Key, k.Layer, k.Path)
	}
	if rerr != nil {
		fmt.Fprintf(errw, "yolo host apply --revert: %v\n", rerr)
		return 1
	}
	// THE PATCHED EXTENSIONS' LINKS (hosttrees.go, docs/design/patched-extensions.md §8.3): each one
	// the render owns goes, and the host's versioned copies with it.
	links, warn, lerr := revertHostTreeLinks(!write)
	if warn != "" {
		pr.Printf("[yellow]Warning: %s[/yellow]", richtext.Escape(warn))
	}
	for _, l := range links {
		action := "would remove the link"
		if write {
			action = "removed the link"
		}
		pr.Printf("  [yellow]%-20s %s[/yellow] [dim]%s[/dim]", "patched extension", action, l)
	}
	if lerr != nil {
		fmt.Fprintf(errw, "yolo host apply --revert: patched extensions: %v — the keys above are withdrawn; "+
			"re-run it to take the links out\n", lerr)
		return 1
	}
	if len(result.Records) == 0 && len(links) == 0 {
		pr.Printf("[dim]yolo has never rendered into this home — nothing to revert.[/dim]")
		return 0
	}
	treeNote := ""
	if len(links) > 0 {
		treeNote = fmt.Sprintf(", and %d patched-extension link(s) with their host copies", len(links))
	}
	if !write {
		pr.Printf("[bold]%d key(s) across %d surface(s)%s[/bold] — re-run with [bold]--assert[/bold] "+
			"to remove them and forget this home.", len(result.Keys), len(result.Records), treeNote)
		pr.Printf("[dim]Your own keys (recorded `host`) are never touched. This removes what " +
			"yolo wrote; it does not restore what a key held before yolo wrote it.[/dim]")
		return 0
	}
	pr.Printf("[green]removed %d key(s) across %d surface(s)%s[/green] — yolo no longer has a "+
		"record of this home.", len(result.Keys), len(result.Records), treeNote)
	pr.Printf("[dim]If you later set host_management to \"own\", its first `yolo host apply " +
		"--assert` is a FIRST apply again, and asks before replacing anything it finds.[/dim]")
	return 0
}

// hostRevertCandidates is the surface set a revert looks at: the packs the config names and
// can resolve, plus every pack yolo SHIPS.
//
// The embedded half is load-bearing HERE in a way it is not elsewhere. A user withdrawing
// yolo has every reason to have already emptied `packs`, and the record lives beside a
// surface its OWNER declares — so without the shipped set a revert after a `packs: []` edit
// would find no surfaces and report a clean home while every key yolo wrote was still in it.
//
// An unresolvable pack (a git pack the pack store does not have, or one whose manifest has
// problems — NS-D14) contributes nothing and is not an error: its keys keep their record and the
// next revert takes them. It is named on errw. For a malformed manifest that is the conservative
// half: a revert removes keys by the surfaces packs declare, and none is read from a manifest
// every launch refuses.
func hostRevertCandidates(errw io.Writer) []*packload.Pack {
	// The one selection function (selectHostPacks, notch-convergence item 6). An unreadable
	// config is reported, not fatal: the shipped set below still covers every surface yolo
	// itself declares, which is where a revert's keys overwhelmingly are.
	sel := selectConfiguredHostPacks()
	for _, u := range sel.problems() {
		// Named, not skipped: its keys keep their record (see above), and the user should know
		// why this revert left them.
		fmt.Fprintf(errw, "yolo host apply --revert: %s could not be resolved, so the keys only "+
			"it declares stay recorded for the next revert: %s\n", u.Name, u.Reason)
	}
	return append(append([]*packload.Pack(nil), sel.packs...), embeddedPacksForPrune()...)
}
