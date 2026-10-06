package image

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Prewarm is the first half of AutoLoadImage, for a launch that has other work to wait on before its
// image step (run's imageprewarm.go: the fork-build slot): the identity evaluated and, unless the
// runtime already holds the stock image it names, the image's derivation built — with nothing
// printed, and nothing loaded, tagged, recorded or rooted. AutoLoadImage, run as ever at the image
// step, then finds the build in the store, and what it reports is its own: a line here would claim
// work the image step's report is the one disclosure of (docs/reference/report-tiers.md).
//
// Every failure is ignored: the image step meets it again and says so. A nix this starts is tracked
// (nixchildren), so a signal that ends the launch stops it; a Ctrl-C at the terminal ends it too,
// and the image step builds again.
func Prewarm(opts AutoLoadOptions) {
	opts.Out, opts.Report = io.Discard, io.Discard
	opts.fill()
	o := &opts
	if o.SkipBuild || o.RepoRoot == "" {
		return
	}
	if identity := o.stockIdentity(); identity != "" && o.stockImageLoaded(identity) != "" {
		return
	}
	outLink := filepath.Join(paths.BuildDir(), fmt.Sprintf("prewarm-result-%d", o.Getpid()))
	defer os.Remove(outLink)
	_, _ = o.BuildStorePath(o.RepoRoot, o.ExtraPackages, outLink)
}
