package run

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE CALL-SITE PIN for the image half's disclosure stream, and it exists because
// the callee-only test is the shape this repo has shipped five times: asserting
// that image.AutoLoadOptions honours Report proves nothing if the run path never
// sets it.
//
// What went wrong without it, on 2026-09-13: the stock-skip line — the one
// disclosure on the path EVERY ordinary launch takes — was written to Out, and on
// this path Out is the jail command's own stdout. `yolo -- bash -c "env | grep …"`
// then returned a provenance sentence glued to the front of the command's output,
// and two integration tests comparing stdout exactly went red on main. `just
// check-ci` could not have caught it: both are integration tests, excluded by
// -short.
//
// Delete the `Report: o.Stderr` line in imageLoadOptions and this fails.
func TestTheRunPathSendsImageDisclosuresToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var got image.AutoLoadOptions
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &stdout
	o.Stderr = &stderr
	o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
		got = opts
		return image.LoadResult{OK: true}
	}

	o.autoLoadImage(jsonx.NewOrderedMap(), "podman", t.TempDir(), storePackagesPlan{})

	if got.Report == nil {
		t.Fatal("imageLoadOptions left Report nil, so the image half falls back to Out — " +
			"which here is the JAIL COMMAND'S STDOUT. Any disclosure on a warm launch " +
			"then corrupts the output of whatever the user asked the jail to run.")
	}
	if got.Report != o.Stderr {
		t.Errorf("Report is not the launch's stderr; every other launch line "+
			"(\"Flake source:\", \"Jail binaries:\") goes there, and a disclosure that "+
			"goes somewhere else is one the human watching the launch never sees. "+
			"got %p, want %p", got.Report, o.Stderr)
	}
	if got.Out != o.Stdout {
		t.Errorf("Out is no longer the command's stdout (%p vs %p) — the progress/"+
			"status lines on the COLD paths are expected there", got.Out, o.Stdout)
	}
}

// TestTheRunPathTellsTheImageLoadWhetherTheJailReadsTheHostStore pins the one
// input that decides what an unrecorded stock-tag match does
// (internal/image/stockimage.go): a jail that will resolve its /bin/* through the
// host /nix/store must build rather than run an image whose closure that store
// cannot be shown to hold. The value must be the assembler's own mount
// predicate, so both directions are asserted.
func TestTheRunPathTellsTheImageLoadWhetherTheJailReadsTheHostStore(t *testing.T) {
	for _, mounted := range []bool{false, true} {
		var got image.AutoLoadOptions
		o := goldenOptions(t.TempDir(), t.TempDir())
		o.PathExists = func(p string) bool {
			return mounted && (p == hostNixSocket || p == hostNixStore)
		}
		o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
			got = opts
			return image.LoadResult{OK: true}
		}
		o.autoLoadImage(jsonx.NewOrderedMap(), "podman", t.TempDir(), storePackagesPlan{})
		if want := o.hostNixMounted("podman"); got.JailReadsHostStore != want || want != mounted {
			t.Errorf("host store mounted=%v: JailReadsHostStore=%v, hostNixMounted=%v", mounted,
				got.JailReadsHostStore, want)
		}
	}
}

// TestTheRunPathHandsTheImageLoadTheGatesStoreFacts pins what makes `yolo capture` and
// every ordinary launch take issue #47's fix WITHOUT a second `podman info`: the run path
// hands AutoLoadImage the store facts parsed from the readiness gate's answer
// (docs/design/podman-reboot-readiness.md PR-D5), so a rootless podman's store reaches the
// destination, and it still hands NO LayerCopy, so the real copy writes where the image load
// computed. The image package's own read ran with no deadline after a probe that had passed;
// a StoreFacts left nil would bring it back with every image-package test still green.
func TestTheRunPathHandsTheImageLoadTheGatesStoreFacts(t *testing.T) {
	var got image.AutoLoadOptions
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout, o.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
		got = opts
		return image.LoadResult{OK: true}
	}
	o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	const info = `{"host":{"security":{"rootless":true}},"store":{"configFile":"/u/.config/containers/storage.conf",` +
		`"graphDriverName":"overlay","graphRoot":"/u/.local/share/containers/storage","runRoot":"/run/user/1000/containers"}}`
	gate := answeringPodman(o, info)
	if _, ok := o.resolveRuntime(nil); !ok {
		t.Fatal("runtime selection refused an answering podman")
	}

	o.autoLoadImage(jsonx.NewOrderedMap(), "podman", t.TempDir(), storePackagesPlan{})

	if got.StoreFacts == nil {
		t.Fatal("the run path left StoreFacts nil, so the image load asks podman again")
	}
	facts := got.StoreFacts()
	if facts.Rootless != image.RootlessYes || !facts.StoreKnown ||
		facts.Store.GraphRoot != "/u/.local/share/containers/storage" {
		t.Errorf("store facts = %+v, want the gate's rootless store", facts)
	}
	if gate.count() != 1 {
		t.Errorf("podman info ran %d times", gate.count())
	}
	if got.LayerCopy != nil {
		t.Error("the run path sets LayerCopy, so the destination the image load computes is " +
			"not the one that is copied to")
	}
}

// With no gate answer (a launch the gate does not cover, which also never copies into a
// store), the facts are the unknown answer — the bare copy, said out loud — and never a
// second `podman info`.
func TestWithNoGateAnswerTheStoreFactsAreUnknown(t *testing.T) {
	o := goldenOptions(t.TempDir(), t.TempDir())
	facts := o.storeFactsFromGate()
	if facts.Rootless != image.RootlessUnknown || facts.StoreKnown || facts.Unknown == "" {
		t.Errorf("facts = %+v, want unknown with a reason", facts)
	}
}
