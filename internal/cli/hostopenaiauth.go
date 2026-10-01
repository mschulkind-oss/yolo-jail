package cli

import (
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
)

type managedOpenAIHostLaunch interface {
	Environ([]string) []string
	// Argv is the managed launch's rewrite of the command, and its disclosure (nil when
	// unchanged): a managed Codex launch adds --no-daemon (openaiauthhost's codexdaemon.go).
	Argv([]string) ([]string, []string)
	Run(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, bool)
	// NotServed is why the launch, not started, leaves a variable unset that a started one sets
	// from a server of its own, "" for any other: a managed Codex launch that did not start for
	// want of a login names the refresh URL (openaiauthhost's notStarted). managedHostVars reads it.
	NotServed(string) string
}

// hostPrelaunch is the declarative OpenAI prelaunch a host launch carries.
type hostPrelaunch = openaiauthhost.Prelaunch

// prepareOpenAIAuthHost runs the launch's declarative OpenAI prelaunch
// (hostComposition.prelaunch); a var so a test can stand in for the broker.
var prepareOpenAIAuthHost = func(p hostPrelaunch, stderr io.Writer) (managedOpenAIHostLaunch, error) {
	launch, err := openaiauthhost.Prepare(p, stderr)
	if launch == nil {
		// A nil *Launch in the interface would read as a managed launch to its callers.
		return nil, err
	}
	return launch, err
}
