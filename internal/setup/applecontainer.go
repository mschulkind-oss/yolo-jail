package setup

import "strings"

// applecontainer.go is THE Apple Container identity probe, which a launch's setup gate and
// `yolo check` both run: the name `container` is generic, so a setup whose backend is Apple
// Container asks the binary on PATH what it is before running it, and a binary that is not
// Apple's is refused with the same next step on both paths.

// IsAppleContainerVersion reports whether out, what `container --version` printed (stdout and
// stderr), is Apple's container CLI: it names Apple, or prints `container CLI version …`.
func IsAppleContainerVersion(out string) bool {
	return strings.Contains(out, "Apple") || strings.Contains(out, "container CLI version")
}

// NotAppleContainerError is a setup whose backend is Apple Container meeting a `container` on
// PATH that is not Apple's.
type NotAppleContainerError struct {
	Setup Setup
	Path  string // the `container` found on PATH
	// Alternative is podman-self when it can run here, "" when it cannot (Podman is not
	// installed, or this OS has no Podman setup): only a setup that runs is offered.
	Alternative string
}

func (e *NotAppleContainerError) Error() string {
	msg := "the 'container' on PATH (" + e.Path + ") is not Apple's container CLI, which setup " +
		e.Setup.Name + " needs: `container --version` names neither Apple nor 'container CLI version'.\n" +
		"  Install Apple Container (" + install[ConfinementAppleContainer].url + ") ahead of it on PATH."
	if e.Alternative != "" {
		msg += "\n  Or use another setup:\n    " + LaunchLine(MustBuiltIn(e.Alternative))
	}
	return msg
}

// CheckAppleContainer is the identity probe for s on goos: nil when s does not run on Apple
// Container or no `container` is on PATH (Buildable reports a missing install), else a
// *NotAppleContainerError when version, asked for the binary lookPath found, did not run or
// does not name Apple's CLI.
func CheckAppleContainer(s Setup, goos string, lookPath func(string) (string, bool),
	version func(bin string) (out string, ran bool)) error {
	if s.Backend() != "container" {
		return nil
	}
	path, ok := lookPath("container")
	if !ok {
		return nil
	}
	if out, ran := version(path); ran && IsAppleContainerVersion(out) {
		return nil
	}
	alt := ""
	if Buildable(MustBuiltIn("podman-self"), goos, func(bin string) bool {
		_, found := lookPath(bin)
		return found
	}) == nil {
		alt = "podman-self"
	}
	return &NotAppleContainerError{Setup: s, Path: path, Alternative: alt}
}
