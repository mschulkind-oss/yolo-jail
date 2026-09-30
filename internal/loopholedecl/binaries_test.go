package loopholedecl_test

// The `binaries` key and its two tokens (binaries.go; docs/design/broker-as-a-pack.md BP-D1):
// an executable a loophole ships as a download pinned by sha256. The refusals are the schema's
// whole value here — a download with no digest, a token in the wrong half or in a field nothing
// substitutes, a name nothing declares — so each is pinned by the words that name its fix.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

var (
	sumA = strings.Repeat("a", 64)
	sumB = strings.Repeat("b", 64)
)

func build(url, sum string) map[string]any { return map[string]any{"url": url, "sha256": sum} }

// toolManifest is a loophole whose host daemon and jail daemon both run one declared binary.
func toolManifest() map[string]any {
	return map[string]any{
		"name": "tool",
		"binaries": map[string]any{
			"toold": map[string]any{
				"linux/amd64":  build("https://example.test/toold-linux-amd64", sumA),
				"darwin/arm64": build("https://example.test/toold-darwin-arm64", sumB),
			},
		},
		"host_daemon": map[string]any{"cmd": []any{"{binary:toold}", "--socket", "{socket}"},
			"publishes": "socket"},
		"jail_daemon": map[string]any{"cmd": []any{"{jail_binary:toold}", "serve"}},
	}
}

func TestBinariesDecodeAndRecordWhichSideNamesThem(t *testing.T) {
	m, err := decodeMap(t, "tool", toolManifest())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Binaries) != 1 || m.Binaries[0].Name != "toold" || len(m.Binaries[0].Builds) != 2 {
		t.Fatalf("Binaries = %+v", m.Binaries)
	}
	bb, ok := m.Binaries[0].BuildFor("linux/amd64")
	if !ok || bb.URL != "https://example.test/toold-linux-amd64" || bb.SHA256 != sumA {
		t.Errorf("BuildFor(linux/amd64) = %+v, %v", bb, ok)
	}
	want := loopholedecl.BinaryRefs{Host: []string{"toold"}, Jail: []string{"toold"}}
	if !reflect.DeepEqual(m.BinaryRefs, want) {
		t.Errorf("BinaryRefs = %+v, want %+v", m.BinaryRefs, want)
	}
	// The argvs are RAW here: resolving the tokens needs the cache and the machine.
	if m.HostDaemon.Cmd[0] != "{binary:toold}" || m.JailDaemon.Cmd[0] != "{jail_binary:toold}" {
		t.Errorf("the decoder substituted a token: host %q jail %q", m.HostDaemon.Cmd, m.JailDaemon.Cmd)
	}
}

// Each side takes the build for where it runs: the machine's own platform for a host reference,
// linux on the machine's arch for a jail one. On an Apple Silicon Mac those are two builds.
func TestBinariesNeededTakesEachSidesPlatform(t *testing.T) {
	m, err := decodeMap(t, "tool", toolManifest())
	if err != nil {
		t.Fatal(err)
	}
	needs := m.BinariesNeeded("darwin", "arm64")
	if len(needs) != 2 {
		t.Fatalf("needs = %+v, want one host and one jail", needs)
	}
	host, jail := needs[0], needs[1]
	if host.Jail || host.Platform != "darwin/arm64" || !host.HasBuild || host.Build.SHA256 != sumB {
		t.Errorf("host need = %+v, want the darwin/arm64 build", host)
	}
	if !jail.Jail || jail.Platform != "linux/arm64" || jail.HasBuild {
		t.Errorf("jail need = %+v, want linux/arm64 with no build declared", jail)
	}
	needs = m.BinariesNeeded("linux", "amd64")
	if !needs[0].HasBuild || !needs[1].HasBuild || needs[1].Build.SHA256 != sumA {
		t.Errorf("on linux/amd64 both sides are the one linux build: %+v", needs)
	}
}

func TestReplaceBinaryTokensReplacesOnlyItsSide(t *testing.T) {
	args := []string{"{binary:a}", "--x={binary:a}", "{jail_binary:a}", "{binary:missing}"}
	path := func(name string) (string, bool) {
		if name == "a" {
			return "/cache/a", true
		}
		return "", false
	}
	got := loopholedecl.ReplaceBinaryTokens(args, false, path)
	want := []string{"/cache/a", "--x=/cache/a", "{jail_binary:a}", "{binary:missing}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("host side = %q, want %q", got, want)
	}
	got = loopholedecl.ReplaceBinaryTokens(args, true, path)
	want = []string{"{binary:a}", "--x={binary:a}", "/cache/a", "{binary:missing}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("jail side = %q, want %q", got, want)
	}
	if args[0] != "{binary:a}" {
		t.Error("ReplaceBinaryTokens changed its input")
	}
}

func TestBinariesRefusals(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(m map[string]any)
		expect string
	}{
		{"no sha256", func(m map[string]any) {
			bins(m)["linux/amd64"] = map[string]any{"url": "https://example.test/x"}
		}, "'binaries.toold.linux/amd64.sha256' is required"},
		{"a digest that is not one", func(m map[string]any) {
			bins(m)["linux/amd64"] = build("https://example.test/x", strings.ToUpper(sumA))
		}, "64 lowercase hex digits"},
		{"plain http", func(m map[string]any) {
			bins(m)["linux/amd64"] = build("http://example.test/x", sumA)
		}, "must be https"},
		{"credentials in the url", func(m map[string]any) {
			bins(m)["linux/amd64"] = build("https://me:secret@example.test/x", sumA)
		}, "carries credentials"},
		{"a relative url", func(m map[string]any) {
			bins(m)["linux/amd64"] = build("toold", sumA)
		}, "is not an absolute URL"},
		{"a GOOS-only platform", func(m map[string]any) {
			bins(m)["linux"] = build("https://example.test/x", sumA)
		}, "both halves"},
		{"an unknown GOOS", func(m map[string]any) {
			bins(m)["linx/amd64"] = build("https://example.test/x", sumA)
		}, "not a known GOOS"},
		{"an unknown GOARCH", func(m map[string]any) {
			bins(m)["linux/amd65"] = build("https://example.test/x", sumA)
		}, "not a known GOARCH"},
		{"no builds", func(m map[string]any) {
			m["binaries"] = map[string]any{"toold": map[string]any{}}
		}, "declares no build"},
		{"a name that is not a file name", func(m map[string]any) {
			m["binaries"] = map[string]any{"../toold": map[string]any{
				"linux/amd64": build("https://example.test/x", sumA)}}
			m["jail_daemon"] = map[string]any{"cmd": []any{"{jail_binary:../toold}"}}
			delete(m, "host_daemon")
		}, "is not a file name"},
		{"a control character in the url", func(m map[string]any) {
			bins(m)["linux/amd64"] = build("https://example.test/x\n[dim]forged", sumA)
		}, "control character"},
		{"a jail token in a host field", func(m map[string]any) {
			m["host_daemon"] = map[string]any{"cmd": []any{"{jail_binary:toold}"}, "publishes": "socket"}
		}, "write '{binary:toold}'"},
		{"a host token in the jail field", func(m map[string]any) {
			m["jail_daemon"] = map[string]any{"cmd": []any{"{binary:toold}"}}
		}, "write '{jail_binary:toold}'"},
		{"a token naming nothing declared", func(m map[string]any) {
			m["jail_daemon"] = map[string]any{"cmd": []any{"{jail_binary:other}"}}
		}, "declares no binary 'other'"},
		{"a token in a field nothing substitutes", func(m map[string]any) {
			m["host_bind_mounts"] = []any{map[string]any{"host": "{binary:toold}", "container": "/x"}}
		}, "which nothing resolves in this field"},
		{"a token in a host daemon's env", func(m map[string]any) {
			hd := m["host_daemon"].(map[string]any)
			hd["env"] = map[string]any{"TOOL": "{binary:toold}"}
		}, "which nothing resolves in this field"},
		{"a declared binary nothing runs", func(m map[string]any) {
			delete(m, "host_daemon")
			delete(m, "jail_daemon")
		}, "no argv names it"},
		{"binaries is not a mapping", func(m map[string]any) {
			m["binaries"] = []any{"toold"}
		}, "'binaries' must be a mapping"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := toolManifest()
			c.edit(m)
			_, err := decodeMap(t, "tool", m)
			if err == nil || !strings.Contains(err.Error(), c.expect) {
				t.Fatalf("err = %v, want one naming %q", err, c.expect)
			}
			// The tolerant decoder refuses the same way: these are structural, not skew.
			if _, _, terr := loopholedecl.DecodeTolerant(manifestBytes(t, m),
				filepath.Join("/loopholes", "tool")); terr == nil {
				t.Error("DecodeTolerant accepted what Decode refused")
			}
		})
	}
}

func bins(m map[string]any) map[string]any {
	return m["binaries"].(map[string]any)["toold"].(map[string]any)
}

// A key inside a build is censused like every other object's: strict refuses a typo, tolerant
// reads past a key only a newer build knows.
func TestBinaryBuildKeysAreCensused(t *testing.T) {
	m := toolManifest()
	b := bins(m)["linux/amd64"].(map[string]any)
	b["size"] = 1234
	if _, err := decodeMap(t, "tool", m); err == nil ||
		!strings.Contains(err.Error(), `binaries.toold.linux/amd64.size`) {
		t.Fatalf("strict decode err = %v, want the unknown build key named", err)
	}
	got, skipped, err := loopholedecl.DecodeTolerant(manifestBytes(t, m), filepath.Join("/loopholes", "tool"))
	if err != nil || got == nil || len(skipped) != 1 {
		t.Fatalf("tolerant decode = %v, %q, %v; want the manifest and one skew note", got, skipped, err)
	}
}

// A pack may ship a binary: nothing in the pack-shipped subset refuses the key, since shipping
// one is the reason it exists (OQ-BP6: a fetched pack's host binary is honored and disclosed).
func TestBinariesArePackShippable(t *testing.T) {
	m, err := decodeMap(t, "tool", toolManifest())
	if err != nil {
		t.Fatal(err)
	}
	if probs := m.PackShippedProblems("/loopholes/tool/manifest.jsonc"); len(probs) != 0 {
		t.Errorf("the pack-shipped subset refuses a binary: %q", probs)
	}
}
