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
// # Refused under `own` and `none`, and for different reasons
//
// Both refusals name the value that decided them, because they are not the same problem: at
// `none` yolo has written nothing to withdraw, and at `own` the file is derived output whose
// removal is a delete-and-re-apply rather than a key-by-key retreat.

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
// operation the user wants, or "" when it may proceed.
//
// SEPARATE FROM hostManagementRefusal, rather than reusing it, because that one's sentences
// are about a render that will not happen and would be actively misleading here: telling
// someone who typed `--revert` that "nothing was written" as though it described this run
// hides the fact that it describes the whole history of the home.
func hostRevertRefusal(mode config.HostManagement) string {
	switch mode {
	case config.HostManagementNone:
		return "`host_management` is \"none\" in " + paths.UserConfigPath() + ", so yolo has " +
			"written nothing into your home and there is nothing to revert.\n" +
			"  If yolo DID write there under an earlier value, set the key back to \"assert\" " +
			"and re-run this to take those keys out."
	case config.HostManagementOwn:
		return "`host_management` is \"own\" in " + paths.UserConfigPath() + ", which says " +
			"these files are derived output — the way out of an owned file is to stop " +
			"declaring it and delete it, not to retreat key by key.\n" +
			"  Set the key to \"assert\" if you want a key-level revert of what yolo asserted."
	}
	return ""
}

// refuseHostRevert stops a revert the declared contract does not permit. EXIT 1, not 2, for
// refuseHostManagement's reason: nothing about the argv is wrong.
func refuseHostRevert(errw io.Writer) (int, bool) {
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
	links, lerr := revertHostTreeLinks(!write)
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
	pr.Printf("[dim]A later `yolo host apply --assert` is a FIRST apply again, and asks " +
		"before replacing anything it finds.[/dim]")
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
