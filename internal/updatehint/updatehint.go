// Package updatehint is the one next step for a file a NEWER yolo wrote: a pack lockfile, a fork
// lock, a capture manifest, a host floor record. Each is refused rather than misread, since an unknown field may change
// what the known ones mean, and the refusal used to end at "upgrade yolo", which leaves the reader
// to work out how this copy was installed (docs/reference/happy-path-principle.md, rule 7, coins
// "next step"). `yolo update` knows: it upgrades a Homebrew or from-source install, prints the
// download link for a release archive, and names the binary's own updater for go install, pipx and
// uv (internal/cli/update.go).
//
// A leaf package, imported by every reader of such a file, so the wording is one sentence in one
// place.
package updatehint

import (
	"fmt"
	"os"
)

// Step is what to run: `yolo update`, and in a jail `yolo update` on the host, then a relaunch. A
// jail's yolo is the host's copy, mounted read-only, and its own `yolo update` only says to run it
// on the host (internal/cli/update.go), so the refusal says that directly. The jail keeps the copy
// it was launched with until it is relaunched, so an update on the host alone would leave this
// jail refusing the same file. YOLO_VERSION is the in-jail signal every other probe reads
// (config.InJail); it is read here because this package sits below config.
func Step() string {
	if os.Getenv("YOLO_VERSION") != "" {
		return InJailStep
	}
	return "run `yolo update`"
}

// InJailStep is Step in a jail, for a caller that has already decided it runs in one: `yolo
// update`'s own refusal there, which reads the environment through its own seam.
const InJailStep = "run `yolo update` on the host and relaunch this jail"

// NewerSchema is the refusal for the file subject names, schema got, when this yolo reads schema
// want and older: a newer yolo wrote it, and Step is the way to that yolo.
func NewerSchema(subject string, got, want int) error {
	return fmt.Errorf("%s: schema %d is newer than this yolo understands (%d), so a newer yolo "+
		"wrote it; %s rather than letting this one misread the file", subject, got, want, Step())
}
