package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// imageCopyVerb is `yolo internal image-copy`: the podman-on-Linux delivery copy
// for a caller OUTSIDE a launch — `just load`, and the by-hand fix the integration
// suite prints for a stale image. Hidden because its callers are a recipe and a
// message, not a person.
//
// It exists so neither caller spells the copy in shell. The copy is TWO decisions
// from one `podman info` read (the namespace prefix, storewrite.go, and the store
// named on the destination, storespec.go), and the shell copies of those drifted
// from the launch's within a day: no driver options, a weaker check of which
// characters a store spec can carry, and a destination of `[@+]` when podman
// reported no store. This verb runs image.DeliveryCopyArgvFor, the argv a launch
// runs, so there is one spelling.
const imageCopyVerb = "image-copy"

func runImageCopy(args []string) int {
	return imageCopyMain(args, os.Stderr, goruntime.GOOS, imageCopyCapture, imageCopyRun)
}

// imageCopyMain is the verb with its effects injected: capture answers `podman
// info`, run executes the copy and returns its exit status.
func imageCopyMain(args []string, stderr io.Writer, goos string,
	capture func(argv []string) (string, bool), run func(argv []string) int) int {
	fs := flag.NewFlagSet("yolo internal "+imageCopyVerb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rt := fs.String("runtime", "podman", "the container runtime whose store is written (podman only)")
	copier := fs.String("copier", "", "the image copier's skopeo binary (.#imageCopier's bin/skopeo)")
	manifest := fs.String("image", "", "the nix2container image.json to copy (.#ociImage)")
	ref := fs.String("ref", "", "the image ref to create, e.g. localhost/yolo-jail:latest")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *copier == "" || *manifest == "" || *ref == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: yolo internal "+imageCopyVerb+
			" [--runtime podman] --copier <skopeo> --image <image.json> --ref <ref>")
		return 2
	}
	// The launch writes containers-storage only for podman on Linux; every other
	// backend takes an archive its own loader reads (autoload.go, deliverViaArchive),
	// and a copy into a store named by a remote podman's `info` would write the
	// Mac's filesystem at the VM's paths.
	if *rt != "podman" || goos != "linux" {
		fmt.Fprintf(stderr, "yolo internal %s: writes podman's store on Linux only "+
			"(runtime %q on %s takes an archive, as a launch does)\n", imageCopyVerb, *rt, goos)
		return 2
	}
	facts := image.ReadPodmanStoreFacts(*rt, capture)
	fmt.Fprintln(stderr, image.StoreWriteNote(facts))
	argv := image.DeliveryCopyArgvFor(*rt, facts, *copier, *manifest, *ref)
	fmt.Fprintln(stderr, "  Copy: "+strings.Join(argv, " "))
	return run(argv)
}

// imageCopyCapture runs argv and returns its stdout; ok=false for anything that did
// not run cleanly.
func imageCopyCapture(argv []string) (string, bool) {
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	return string(out), err == nil
}

// imageCopyRun runs argv on this process's stdio and returns its exit status.
//
// The copier is a Nix store closure, so it runs without the caller's
// LD_LIBRARY_PATH/LD_PRELOAD (execx.NixClosureCommand) — and so does a
// `podman unshare --` prefix, whose environment is the copier's
// (image-staging-vs-baking.md, LI-D1).
func imageCopyRun(argv []string) int {
	cmd := execx.NixClosureCommand(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "yolo internal %s: %v\n", imageCopyVerb, err)
	return 1
}
