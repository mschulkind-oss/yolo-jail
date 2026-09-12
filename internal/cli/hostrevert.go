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
	if rerr != nil {
		fmt.Fprintf(errw, "yolo host apply --revert: %v\n", rerr)
		return 1
	}
	if len(result.Records) == 0 {
		pr.Printf("[dim]yolo has never rendered into this home — nothing to revert.[/dim]")
		return 0
	}
	if !write {
		pr.Printf("[bold]%d key(s) across %d surface(s)[/bold] — re-run with [bold]--assert[/bold] "+
			"to remove them and forget this home.", len(result.Keys), len(result.Records))
		pr.Printf("[dim]Your own keys (recorded `host`) are never touched. This removes what " +
			"yolo wrote; it does not restore what a key held before yolo wrote it.[/dim]")
		return 0
	}
	pr.Printf("[green]removed %d key(s) across %d surface(s)[/green] — yolo no longer has a "+
		"record of this home.", len(result.Keys), len(result.Records))
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
// An unresolvable pack (a fetched one with an offline remote) contributes nothing and is not
// an error: its keys keep their record and the next revert takes them.
func hostRevertCandidates(errw io.Writer) []*packload.Pack {
	var loaded []*packload.Pack
	entries, err := config.LoadPacks(nil)
	if err != nil {
		// Reported, not fatal: the shipped set below still covers every surface yolo itself
		// declares, which is where a revert's keys overwhelmingly are.
		fmt.Fprintf(errw, "yolo host apply --revert: reading `packs`: %v\n", err)
	}
	for _, e := range entries {
		if p := packForCheckDeps(e); p != nil {
			loaded = append(loaded, p)
		}
	}
	return append(loaded, embeddedPacksForPrune()...)
}
