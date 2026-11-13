package run

// pinnedexec.go is the run package's use of the launch's PINNED HELPER: the one host yolo a launch
// execs every helper that serves its jail from (docs/reference/pinned-host-helpers.md, PH-D3 to PH-D7).
// A launch resolves it once at pre-flight (resolveHelperPin) from the bundle its repo root names;
// the keeper receives it through its plan; every self-exec in this package goes through
// Options.selfExecArgv, which execs it after a stat check and NEVER falls back to os.Executable()
// (P2: a fallback is the different-build restart this removes).

import (
	"errors"
	"fmt"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// errNoHelperPin is a self-exec asked for by a launch that resolved no pin: a launch that starts a
// helper resolves one at pre-flight, so reaching this is a path that skipped it.
var errNoHelperPin = fmt.Errorf("this launch resolved no host yolo to start its helpers from, and does not fall "+
	"back to the running launcher, which may be another build. %s", reporoot.ReinstallSteps)

// resolveHelperPin is the pre-flight (PH-D3): the pin of the bundle at bundleRoot, stored on the
// launch. A launch that starts no helper (a dry run) resolves none. An error is the refusal, which
// already names what was missing and the reinstall step.
func (o *Options) resolveHelperPin(bundleRoot string) error {
	pin, err := helperpin.Resolve(bundleRoot)
	if err != nil {
		return err
	}
	o.helperPin = &pin
	if o.MacosUserArm != nil { // the backend stages and starts from the same pin the pipeline does
		o.MacosUserArm.SetHelperPin(&pin)
	}
	return nil
}

// pinOrZero is the pin a keeper's plan carries: the launch's, or the zero Pin for a launch that
// has none (a test's, which never execs).
func (o *Options) pinOrZero() helperpin.Pin {
	if o.helperPin == nil {
		return helperpin.Pin{}
	}
	return *o.helperPin
}

// pinnedArgv is argv with its leading "yolo" replaced by the pin's path, after the stat check of
// "The check before every exec". A missing or changed pin is an error naming the path, never a fallback.
func (o *Options) pinnedArgv(argv []string) ([]string, error) {
	pin := o.helperPin
	if pin == nil || pin.Path == "" {
		return nil, errNoHelperPin
	}
	if !pin.Unchanged() {
		return nil, o.pinGoneError(pin.Path)
	}
	out := slices.Clone(argv)
	out[0] = pin.Path
	return out, nil
}

// pinGoneError says the pinned yolo is no longer at path and the next step: in a keeper, the stop
// step of its jail (keyedStopRemedy), whose launch is long past; in a launch, the reinstall step.
func (o *Options) pinGoneError(path string) error {
	if o.keeperMode {
		return errors.New(pinGoneClause(path) + " It stays down until the jail is launched again: " +
			o.stopStep() + ".")
	}
	return fmt.Errorf("%s %s", pinGoneClause(path), reporoot.ReinstallSteps)
}

// stopStep is the stop step a give-up line names: this launch's own when it has no jail
// (helperStopRemedy, `yolo host`), else the one keyedStopRemedy words for its notch.
func (o *Options) stopStep() string {
	if o.helperStopRemedy != "" {
		return o.helperStopRemedy
	}
	return keyedStopRemedy(o.runtime, o.jailName())
}

// pinGoneClause is the sentence shared by every report of a pin that is no longer there.
func pinGoneClause(path string) string {
	return "the yolo this jail was launched with is no longer at " + path + " (an upgrade removed or replaced it)."
}

// selfExecArgv resolves a leading "yolo" to the binary to run. A KEEPER on Linux uses its own
// inode (keeperSelfExe, /proc/self/exe): it can outlive its launch by days across a `just install`,
// and because it was spawned from the pin, that inode IS the pin's (JL-D5, PH-D5). Everything else
// execs the pin by path, checked first. A command that is not the bare token is left untouched.
func (o *Options) selfExecArgv(argv []string) ([]string, error) {
	return o.selfExecArgvWith(keeperSelfExe(), argv)
}

// selfExecArgvWith is selfExecArgv with the platform's keeper self-exec path given: "" is a
// platform with no /proc/self/exe.
func (o *Options) selfExecArgvWith(keeperExe string, argv []string) ([]string, error) {
	if len(argv) == 0 || argv[0] != "yolo" {
		return argv, nil
	}
	if o.keeperMode && keeperExe != "" {
		return append([]string{keeperExe}, argv[1:]...), nil
	}
	return o.pinnedArgv(argv)
}

// ensureHelperPin resolves a pin for a caller that is no launch (`yolo config apply`'s context
// injector) and finds none on its Options: from the bundle its own repo root names, as a launch's
// pre-flight does. It never reaches for os.Executable().
func (o *Options) ensureHelperPin() error {
	if o.helperPin != nil {
		return nil
	}
	if o.RepoRoot == nil {
		return errNoHelperPin
	}
	res, ok := o.RepoRoot()
	if !ok {
		if res.Error != nil {
			return res.Error
		}
		return errNoHelperPin
	}
	return o.resolveHelperPin(res.Root)
}

// reportPinnedSpawnRefused says a spawn of what did not happen because its pinned yolo is gone or
// was never resolved, and returns err. In a keeper it is the one line of the "check before every exec" table's second row
// (the path and the next step); in a launch it is the refusal the caller returns. Never a fallback.
func (o *Options) reportPinnedSpawnRefused(what string, err error) error {
	if o.keeperMode {
		o.pr(o.Stderr).print("[yellow]Not starting " + what + ": " + richtext.Escape(err.Error()) + "[/yellow]")
	}
	return fmt.Errorf("not starting %s: %w", what, err)
}
