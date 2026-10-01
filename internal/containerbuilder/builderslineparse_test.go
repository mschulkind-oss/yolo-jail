package containerbuilder

import (
	"net/url"
	"strings"
	"testing"
)

// builderslineparse_test.go reads a --builders line the way nix does, so a test can ask what
// NIX would take from it rather than which bytes it holds.
//
// nix 2.34's machines.cc: the setting is split into machines on '\n' and ';', a machine is cut
// at its first '#', trimmed, and tokenized on " \t\n\r"; a field that is "" or "-" is unset.
// The store URI (field 1) is parsed as a URL whose query values are percent-decoded, and an
// unset key field leaves the URI's own ssh-key in place. Measured 2026-10-01 against nix
// 2.34.8 (a throwaway chroot store, --max-jobs 0, a fake ssh on PATH recording its argv): for
// key paths holding a space, a ';', a '#', a '%' and a '+', the line BuildersLine writes reached
// ssh as `-i <that path>`, one argument, beside the pinned known-hosts file. The line it wrote
// before (the path in field 3) was refused for the space ("bad machine specification: failed to
// convert column #3 … to 'unsigned int'"), and for the ';' and the '#' handed ssh the path cut
// at that byte, with no known-hosts pin at all.

// nixMachine is one machine of a parsed --builders line.
type nixMachine struct {
	fields []string // the whitespace-separated fields, as nix tokenized them
	base   string   // the store URI without its query
	system string
	key    string // the ssh key nix hands ssh: field 3 when set, else the URI's ssh-key
}

// parseBuildersAsNix parses builders as nix does, failing the test unless it holds exactly
// one machine.
func parseBuildersAsNix(t *testing.T, builders string) nixMachine {
	t.Helper()
	var machines []string
	for _, m := range strings.FieldsFunc(builders, func(r rune) bool { return r == '\n' || r == ';' }) {
		if i := strings.IndexByte(m, '#'); i >= 0 {
			m = m[:i]
		}
		if m = strings.TrimSpace(m); m != "" {
			machines = append(machines, m)
		}
	}
	if len(machines) != 1 {
		t.Fatalf("nix reads %d machines from %q, want 1: %q", len(machines), builders, machines)
	}
	fields := strings.FieldsFunc(machines[0], func(r rune) bool { return strings.ContainsRune(" \t\n\r", r) })
	set := func(i int) bool { return len(fields) > i && fields[i] != "" && fields[i] != "-" }
	m := nixMachine{fields: fields}
	base, query, _ := strings.Cut(fields[0], "?")
	m.base = base
	for _, kv := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k != "ssh-key" {
			continue
		}
		// PathUnescape and not QueryUnescape: nix decodes %XX only, and leaves a '+' a '+'.
		dec, err := url.PathUnescape(v)
		if err != nil {
			t.Fatalf("nix cannot percent-decode the URI's ssh-key %q: %v", v, err)
		}
		m.key = dec
	}
	if set(1) {
		m.system = fields[1]
	}
	if set(2) {
		m.key = fields[2]
	}
	return m
}

// A KEY PATH NIX WOULD SPLIT STILL REACHES SSH WHOLE. The key lives under the state dir in the
// user's home, so a home or state path holding a space put the space in field 3, every field
// after it moved one place right, and nix refused the line, killing the macOS build offload.
// A ';' or a '#' cut the line short instead. Each spelling must leave nix with the same eight
// fields and the key the caller named, with the host key still in field 8.
func TestBuildersLineCarriesAKeyPathNixWouldSplit(t *testing.T) {
	const hostKey = "c3NoLWVkMjU1MTkgQUFBQQ=="
	for _, key := range []string{
		"/keys/id_ed25519",
		"/Users/Jane Doe/.local/share/yolo-jail/linux-builder-container/id_ed25519",
		"/Users/x/Library/Application Support/yolo/id_ed25519",
		"/tmp/tab\there/id",
		"/tmp/semi;colon/id",
		"/tmp/hash#mark/id",
		"/tmp/per%cent and+plus/id",
	} {
		line := BuildersLine("127.0.0.1", 31022, 4, key, hostKey)
		m := parseBuildersAsNix(t, line)
		if len(m.fields) != 8 {
			t.Errorf("key %q: nix reads %d fields, want 8 so the host key stays in field 8: %q",
				key, len(m.fields), line)
			continue
		}
		if m.key != key {
			t.Errorf("key %q: nix hands ssh the key %q: %q", key, m.key, line)
		}
		if m.base != "ssh-ng://root@127.0.0.1:31022" || m.system != BuilderSystem() ||
			m.fields[3] != "4" || m.fields[7] != hostKey {
			t.Errorf("key %q: the other fields moved: %q", key, line)
		}
	}
}

// The session's own line, from the key under THIS process's home, is one nix reads whole too:
// the call site, not only the function, carries a home with a space.
func TestSessionBuildersLineUnderAHomeWithASpace(t *testing.T) {
	t.Setenv("HOME", "/Users/Jane Doe")
	s := &Session{Runtime: "podman", hostKey: "c3NoLWVkMjU1MTkgQUFBQQ=="}
	m := parseBuildersAsNix(t, s.BuildersLine("127.0.0.1", BuilderHostPort, 4))
	if m.key != BuilderKey() || !strings.HasPrefix(m.key, "/Users/Jane Doe/") {
		t.Errorf("nix hands ssh the key %q, want %q", m.key, BuilderKey())
	}
	if len(m.fields) != 8 {
		t.Errorf("nix reads %d fields, want 8: %q", len(m.fields), m.fields)
	}
}
