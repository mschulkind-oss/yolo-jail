package run

// imageprewarm.go starts a fresh launch's own IMAGE BUILD beside its fork-build slot
// (docs/design/pi-extension-store-builds.md XB-D57): the slot's keys can take minutes and the image
// waits for none of them — what the slot decides reaches the jail as mounts and an env var
// assembled after the image step — so the identity's eval and, when the runtime does not hold the
// image already, its nix build run while the slot does, and the image step that follows finds them
// done.
//
// WHAT RUNS BESIDE THE SLOT IS SILENT AND OPTIONAL: image.Prewarm prints nothing and loads, tags and
// roots nothing, and its failures are ignored, so the image step runs exactly as before, in its
// place, saying everything it says — only faster. A Ctrl-C that ends the slot's wait reaches the
// prewarm's nix too, as the terminal's whole foreground group, and the image step then builds again.
// A signal that ends the launch stops it with every other nix of the launch (nixchildren).
//
// THE IDENTITY IS EVALUATED ONCE for the launch (identityMemo): the image step asks the eval the
// prewarm already ran, or waits for the one it is running, rather than evaluating again.
//
// WHERE IT DOES NOT RUN, every case one where the image step's request is not known before the slot
// or no image is built: a launch with the store-delivered packages opted in (whose plan the image
// step settles after the slot), on a macOS host (whose image build may need a Linux builder the
// image step starts), on macos-user (no image), a dry run, a capture or build jail's own launch
// (which has no slot to run beside), and a launch whose image step is a test's fake.

import (
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// startImagePrewarm starts the prewarm when this launch's image request is known now, and returns
// at once.
func (o *Options) startImagePrewarm(cfg *jsonx.OrderedMap, rt, repoRoot string) {
	prewarm := o.prewarmImage
	if prewarm == nil {
		if o.autoLoad != nil {
			return // the image step is a fake: there is nothing to warm
		}
		prewarm = image.Prewarm
	}
	if rt == "macos-user" { // parity: NotApplicable — macos-user builds and loads no image to warm
		return
	}
	if o.IsMacOS || o.DryRun || o.subLaunch() || repoRoot == "" || envTruthy(o.Getenv(StorePackagesOptInEnv)) {
		return
	}
	o.imageIdentity = &identityMemo{}
	opts := o.imageLoadOptions(cfg, rt, repoRoot, storePackagesPlan{})
	opts.Perf = nil // its spans would read as the image step's
	go safeRun(func() { prewarm(opts) })
}

// identityMemo is one launch's image identity, evaluated by whichever of the prewarm and the image
// step asks first; the other takes that answer, waiting for it while it runs. An eval that failed is
// not kept: the next to ask evaluates again, as the image step would have (a Ctrl-C at the terminal
// fails the prewarm's).
type identityMemo struct {
	mu      sync.Mutex
	running chan struct{} // closed when the eval in flight ends; nil before the first
	id      string
	ok      bool
}

// eval is image.AutoLoadOptions.EvalIdentity for this launch.
func (m *identityMemo) eval(repoRoot string) (string, bool) {
	m.mu.Lock()
	if m.running == nil || (!m.ok && isClosed(m.running)) {
		running := make(chan struct{})
		m.running = running
		m.mu.Unlock()
		id, ok := identityEval(repoRoot)
		m.mu.Lock()
		m.id, m.ok = id, ok
		m.mu.Unlock()
		close(running)
		return id, ok
	}
	running := m.running
	m.mu.Unlock()
	<-running
	m.mu.Lock()
	id, ok := m.id, m.ok
	m.mu.Unlock()
	if !ok {
		return identityEval(repoRoot)
	}
	return id, ok
}

// identityEval is the eval itself: a var so a test can count evals.
var identityEval = image.EvalImageIdentity

// isClosed reports whether c is closed, without blocking.
func isClosed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}
