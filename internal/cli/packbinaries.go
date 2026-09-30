package cli

// packbinaries.go is `yolo pack install`'s DOWNLOAD STEP: every build of a declared loophole
// binary (internal/loopholedecl's binaries.go) the selected packs need on this machine, fetched,
// verified against the sha256 its manifest pins, and cached by digest with its exec bit set
// (internal/packbin; docs/design/broker-as-a-pack.md BP-D1).
//
// THIS IS THE ONLY FETCH. A launch reads the cache and never downloads (§3.1: "at `pack
// install`, never at launch"), and says `yolo pack install` when a build it needs is missing
// (loopholes.BinaryInertNotes). `yolo pack update` runs install first, so it fetches too.
//
// EVERY SELECTED PACK, embedded ones included, through the one selection function the host verbs
// share (selectConfiguredHostPacks). An embedded pack is the case the mechanism exists for — its
// tree cannot carry an exec bit — and it has nothing to fetch but its binaries, so walking only
// the fetched packs would leave exactly the packs yolo ships unable to run one.
//
// WHATEVER THE LOOPHOLE'S SWITCH SAYS. Installing a pack installs what it ships; a loophole
// switched on later then runs without another network step, which is what install is for.

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// packBinaryFetcher is the cache `yolo pack install` fills. A seam so a test can serve the
// builds from a local TLS server; production fetches into paths.PackBinariesDir.
var packBinaryFetcher = func() packbin.Fetcher {
	return packbin.Fetcher{Dir: paths.PackBinariesDir()}
}

// installPackBinaries is the download step over the user's selection, on this machine.
func installPackBinaries(pr richtext.Printer, errw io.Writer) int {
	set := selectConfiguredHostPacks()
	return fetchPackBinaries(set.packs, packBinaryFetcher(), runtime.GOOS, runtime.GOARCH, pr, errw)
}

// fetchPackBinaries ensures every build packs need on a goos/goarch machine is in f's cache, and
// reports one line per build: fetched, already cached, re-fetched, or missing for this platform.
// It returns 1 when any build could not be fetched or verified, 0 otherwise. A build two
// loopholes pin with the same digest and name is fetched once.
func fetchPackBinaries(packs []*packload.Pack, f packbin.Fetcher, goos, goarch string,
	pr richtext.Printer, errw io.Writer) int {
	rc := 0
	done := map[string]bool{}
	for _, p := range packs {
		mods, _, _ := p.LoopholeModules()
		for _, mod := range mods {
			if mod.Decl == nil {
				continue // an unreadable manifest is reported where the pack is loaded
			}
			for _, n := range mod.Decl.BinariesNeeded(goos, goarch) {
				label := fmt.Sprintf("%s: loophole %s: binary %s for %s", p.Name, mod.Name,
					n.Binary, n.Platform)
				if done[label] {
					continue
				}
				done[label] = true
				if !n.HasBuild {
					pr.Printf("[yellow]%s: no build is declared, so the loophole does nothing "+
						"on this machine[/yellow]", label)
					continue
				}
				key := n.Build.SHA256 + "\x00" + n.Binary
				if done[key] {
					continue
				}
				done[key] = true
				_, outcome, err := f.Ensure(context.Background(), packbin.Want{
					Name: n.Binary, URL: n.Build.URL, SHA256: n.Build.SHA256})
				if err != nil {
					fmt.Fprintf(errw, "yolo pack install: %s: %v\n", label, err)
					rc = 1
					continue
				}
				switch outcome {
				case packbin.Cached:
					pr.Printf("[dim]%s: already fetched (sha256 %s)[/dim]", label, shortSHA(n.Build.SHA256))
				case packbin.Replaced:
					pr.Printf("[yellow]%s: the cached copy no longer matched its sha256, so it was "+
						"fetched again and verified (sha256 %s)[/yellow]", label, shortSHA(n.Build.SHA256))
				default:
					pr.Printf("[green]%s: fetched and verified (sha256 %s)[/green]", label,
						shortSHA(n.Build.SHA256))
				}
			}
		}
	}
	return rc
}
