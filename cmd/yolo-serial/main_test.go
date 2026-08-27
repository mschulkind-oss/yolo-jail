package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNoArgsPrintsUsage(t *testing.T) {
	rc := run([]string{})
	if rc != 2 {
		t.Errorf("run([]) = %d, want 2", rc)
	}
}

func TestHelpFlag(t *testing.T) {
	rc := run([]string{"--help"})
	if rc != 0 {
		t.Errorf("run([--help]) = %d, want 0", rc)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	rc := run([]string{"unknown-cmd"})
	if rc != 2 {
		t.Errorf("run([unknown-cmd]) = %d, want 2", rc)
	}
}

func TestNoEndpointMsg(t *testing.T) {
	var buf bytes.Buffer
	noEndpointMsg(&buf)
	msg := buf.String()
	if !strings.Contains(msg, "serial") || !strings.Contains(msg, "loopholes") {
		t.Errorf("noEndpointMsg output unexpected: %s", msg)
	}
}

func TestOpenPty(t *testing.T) {
	master, slavePath, err := openPty()
	if err != nil {
		t.Fatalf("openPty failed: %v", err)
	}
	defer master.Close()
	if !strings.HasPrefix(slavePath, "/dev/pts/") {
		t.Errorf("slavePath = %q, want /dev/pts/...", slavePath)
	}
}
