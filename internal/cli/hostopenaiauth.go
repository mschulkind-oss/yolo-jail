package cli

import (
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
)

type managedOpenAIHostLaunch interface {
	Environ([]string) []string
	Run(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, bool)
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
