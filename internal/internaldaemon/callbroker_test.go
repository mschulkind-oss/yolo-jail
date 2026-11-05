package internaldaemon

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestCallBrokerCoreDaemonHasTheProductionDispatch(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = write
	defer func() { os.Stderr = original; read.Close(); write.Close() }()
	code := Run([]string{"call-broker"})
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || !strings.Contains(string(output), "call-broker: --socket and --launch-input are required") {
		t.Fatalf("core daemon dispatch: exit %d, message %q", code, output)
	}
}
