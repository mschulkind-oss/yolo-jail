package nixroots

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixroots/nixrootstest"
)

// The client against the fake daemon in each dialect it can meet: 1.38 (the measured host,
// answered as 1.37), 1.32 (no version string, no trust flag), and 1.25 (the old error frame),
// with and without the log frames a real daemon may interleave.
func TestAddIndirectRootSendsThePathVerbatim(t *testing.T) {
	for _, proto := range []uint64{1<<8 | 38, 1<<8 | 35, 1<<8 | 32, 1<<8 | 25, 1<<8 | 14} {
		for _, chatter := range []bool{false, true} {
			d := nixrootstest.Start(t, nixrootstest.Options{Protocol: proto, Chatter: chatter})
			const path = "/home/u/code/proj with space/.yolo/home/local/share/yolo-jail/build/roots/0123456789abcdef"
			if err := AddIndirectRoot(d.Socket, path, 5*time.Second); err != nil {
				t.Fatalf("protocol 1.%d, chatter %v: %v", proto&0xff, chatter, err)
			}
			if got := d.Roots(); !slices.Equal(got, []string{path}) {
				t.Errorf("protocol 1.%d, chatter %v: daemon recorded %q, want exactly %q",
					proto&0xff, chatter, got, path)
			}
		}
	}
}

// A daemon's refusal is the one failure the design says is worth a line, so it must come
// back as its own type, carrying the daemon's message, in both error dialects.
func TestAddIndirectRootReportsARefusalAsRejected(t *testing.T) {
	for _, proto := range []uint64{1<<8 | 38, 1<<8 | 25} {
		d := nixrootstest.Start(t, nixrootstest.Options{Protocol: proto, Reject: "you may not", Chatter: true})
		err := AddIndirectRoot(d.Socket, "/h/x", 5*time.Second)
		var rej *RejectedError
		if !errors.As(err, &rej) {
			t.Fatalf("protocol 1.%d: err = %v, want a *RejectedError", proto&0xff, err)
		}
		if rej.Msg != "you may not" {
			t.Errorf("protocol 1.%d: refusal message = %q", proto&0xff, rej.Msg)
		}
		if len(d.Roots()) != 0 {
			t.Errorf("a refused root was recorded: %q", d.Roots())
		}
	}
}

// No daemon is an ordinary error, never a refusal: it is today's state, and says nothing.
func TestAddIndirectRootWithNoDaemonIsNotARefusal(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "nrn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	err = AddIndirectRoot(filepath.Join(dir, "absent"), "/h/x", time.Second)
	var rej *RejectedError
	if err == nil || errors.As(err, &rej) {
		t.Errorf("err = %v, want a plain error", err)
	}
}
