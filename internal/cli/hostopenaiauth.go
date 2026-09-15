package cli

import (
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
)

type managedOpenAIHostLaunch interface {
	Environ([]string) []string
	Run(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, bool)
}

var prepareOpenAIAuthHost = func(agent string, stderr io.Writer) (managedOpenAIHostLaunch, error) {
	return openaiauthhost.Prepare(agent, stderr)
}
