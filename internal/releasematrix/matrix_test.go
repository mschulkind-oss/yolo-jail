package releasematrix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

var allFour = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"}

// The matrix is read from the release's own config, never copied: this is the file goreleaser
// runs on every tag.
func TestReleasePlatformsAreTheYoloBuildsOfTheRealConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", GoreleaserConfig))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReleasePlatforms(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, allFour) {
		t.Errorf("ReleasePlatforms(.goreleaser.yaml) = %v, want %v — if the release really "+
			"changed its platforms, BP-D7 moves with it and so does every official binary",
			got, allFour)
	}
}

func TestReleasePlatformsReadsAnchorsAndIgnore(t *testing.T) {
	cfg := `
builds:
  - id: other
    goos: [windows]
    goarch: [amd64]
  - id: yolo
    goos: &oses
      - linux
      - darwin
    goarch: &arches [amd64, arm64, riscv64]
    ignore:
      - goos: darwin
        goarch: riscv64
      - goarch: arm64
`
	got, err := ReleasePlatforms([]byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"darwin/amd64", "linux/amd64", "linux/riscv64"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReleasePlatforms = %v, want %v", got, want)
	}
}

// What the reader does not model it refuses, naming it, rather than guessing.
func TestReleasePlatformsRefusesWhatItDoesNotModel(t *testing.T) {
	for _, tc := range []struct{ cfg, want string }{
		{"builds:\n  - id: other\n    goos: [linux]\n    goarch: [amd64]\n", `no build with id "yolo"`},
		{"builds:\n  - id: yolo\n    goarch: [amd64]\n", "goos and goarch explicitly"},
		{"builds:\n  - id: yolo\n    targets: [linux_amd64]\n", "`targets`"},
		{"builds:\n  - id: yolo\n    goos: [linux]\n    goarch: [arm]\n    ignore:\n      - goarm: \"6\"\n", `ignores by "goarm"`},
		{"builds: [", "yaml"},
	} {
		_, err := ReleasePlatforms([]byte(tc.cfg))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ReleasePlatforms(%q) = %v, want an error naming %q", tc.cfg, err, tc.want)
		}
	}
}

// decode builds a loophole manifest from a map, through the strict decoder.
func decode(t *testing.T, name string, data map[string]any) *loopholedecl.Manifest {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loopholedecl.Decode(raw, filepath.Join("/loopholes", name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func buildOf(name, version, platform string) map[string]any {
	return map[string]any{"url": AssetURL(name, version, platform), "sha256": strings.Repeat("0", 64)}
}

// fixtureManifest is a loophole named `tool` whose argvs name `toold` on the sides given, with
// a build declared for each of platforms and the loophole's own `platforms` set to supports
// (omitted when nil).
func fixtureManifest(host, jail bool, supports []string, builds ...string) map[string]any {
	bs := map[string]any{}
	for _, p := range builds {
		bs[p] = buildOf("toold", "1.2.3", p)
	}
	m := map[string]any{"name": "tool", "binaries": map[string]any{"toold": bs}}
	if host {
		m["host_daemon"] = map[string]any{"cmd": []any{"{binary:toold}", "{socket}"}, "publishes": "socket"}
	}
	if jail {
		m["jail_daemon"] = map[string]any{"cmd": []any{"{jail_binary:toold}"}}
	}
	if supports != nil {
		s := make([]any, len(supports))
		for i, p := range supports {
			s[i] = p
		}
		m["platforms"] = s
	}
	return m
}

// BP-D7, case by case: a host reference wants every release platform its loophole admits, a
// jail reference wants linux on each of their architectures, and nothing wants a darwin jail
// build.
func TestPlatformsIsBPD7(t *testing.T) {
	for _, tc := range []struct {
		name       string
		host, jail bool
		supports   []string
		want       []string
	}{
		{"host, everywhere", true, false, nil, allFour},
		{"jail, everywhere", false, true, nil, []string{"linux/amd64", "linux/arm64"}},
		{"both, everywhere", true, true, nil, allFour},
		{"host, linux only", true, false, []string{"linux"}, []string{"linux/amd64", "linux/arm64"}},
		{"jail, one mac", false, true, []string{"darwin/arm64"}, []string{"linux/arm64"}},
		{"host, one mac", true, false, []string{"darwin/arm64"}, []string{"darwin/arm64"}},
		{"host, a platform the release lacks", true, false, []string{"windows"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := decode(t, "tool", fixtureManifest(tc.host, tc.jail, tc.supports, "linux/amd64"))
			if got := Platforms(m, "toold", allFour); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Platforms = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAssetURLIsBPD8AndParsesBack(t *testing.T) {
	u := AssetURL("oauth-terminator", "0.12.0", "darwin/arm64")
	want := "https://github.com/mschulkind-oss/yolo-jail/releases/download/v0.12.0/oauth-terminator_0.12.0_darwin_arm64"
	if u != want {
		t.Fatalf("AssetURL = %q, want %q", u, want)
	}
	for _, tc := range []struct{ name, version, platform string }{
		{"oauth-terminator", "0.12.0", "darwin/arm64"},
		{"my_tool", "1.0.0-rc.1", "linux/amd64"},
	} {
		n, v, p, ok := ParseAssetURL(AssetURL(tc.name, tc.version, tc.platform))
		if !ok || n != tc.name || v != tc.version || p != tc.platform {
			t.Errorf("ParseAssetURL(AssetURL(%v)) = %q %q %q %v", tc, n, v, p, ok)
		}
	}
	for _, bad := range []string{
		"https://example.test/v0.12.0/t_0.12.0_linux_amd64",
		"https://github.com/mschulkind-oss/yolo-jail/releases/download/v0.12.0/t_0.11.0_linux_amd64",
		"https://github.com/mschulkind-oss/yolo-jail/releases/download/0.12.0/t_0.12.0_linux_amd64",
		"https://github.com/mschulkind-oss/yolo-jail/releases/download/v0.12/t_0.12_linux_amd64",
		"https://github.com/mschulkind-oss/yolo-jail/releases/download/v0.12.0/x/t_0.12.0_linux_amd64",
		"https://github.com/mschulkind-oss/yolo-jail/releases/download/v0.12.0/_0.12.0_linux_amd64",
		"https://github.com/mschulkind-oss/yolo-jail/releases/download/v0.12.0/t_0.12.0_linux",
	} {
		if n, v, p, ok := ParseAssetURL(bad); ok {
			t.Errorf("ParseAssetURL(%q) = %q %q %q, want refused", bad, n, v, p)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{"0.12.0": "0.12.0", "v0.12.0": "0.12.0", "1.0.0-rc.1": "1.0.0-rc.1"} {
		if got, err := NormalizeVersion(in); err != nil || got != want {
			t.Errorf("NormalizeVersion(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0.12", "latest", "v", "0.12.0/x"} {
		if _, err := NormalizeVersion(bad); err == nil {
			t.Errorf("NormalizeVersion(%q) accepted it", bad)
		}
	}
}
