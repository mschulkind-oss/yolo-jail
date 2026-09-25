package oauthbroker

// credslink_test.go pins that the broker never follows a link the jail planted at the shared
// credentials file (docs/reference/jail-home.md, "Host code in jail-writable state"). The broker
// runs on the HOST, as the host user, and the file sits in the machine store's
// `.claude-shared-credentials` dir, which every Claude jail binds read-write. So the jail can
// put a link at `.credentials.json` naming any host file, the host user's own
// `~/.claude/.credentials.json` included, and a broker reading through it SERVED that file's
// tokens to every jail that asked.
//
// Each case plants the link, runs the production entry point, and checks what came back.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const hostAccessToken = "host-only-access-token"

// hostCredsFile is a host credentials file (a stand-in for the host user's own
// ~/.claude/.credentials.json) carrying a token that is fresh for hours.
func hostCredsFile(t *testing.T, now time.Time) (path, body string) {
	t.Helper()
	body = `{"claudeAiOauth":{"accessToken":"` + hostAccessToken + `","refreshToken":"host-only-refresh",` +
		`"expiresAt":` + strconv.FormatInt(now.Add(8*time.Hour).UnixMilli(), 10) + `}}`
	path = filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, body
}

// credsLinkShapes plants a link at the shared creds path: to an existing host creds file, and
// dangling. verify checks the host side behind it was left alone.
var credsLinkShapes = []struct {
	name  string
	plant func(t *testing.T, credsPath string, now time.Time) (verify func(t *testing.T))
}{
	{"a link to an existing host file", func(t *testing.T, credsPath string, now time.Time) func(*testing.T) {
		host, body := hostCredsFile(t, now)
		if err := os.Symlink(host, credsPath); err != nil {
			t.Fatal(err)
		}
		return func(t *testing.T) {
			t.Helper()
			if got, err := os.ReadFile(host); err != nil || string(got) != body {
				t.Errorf("the broker wrote through the jail's link into the host file: %q (err %v)", got, err)
			}
		}
	}},
	{"a dangling link", func(t *testing.T, credsPath string, _ time.Time) func(*testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(dir, "created-by-the-follow"), credsPath); err != nil {
			t.Fatal(err)
		}
		return func(t *testing.T) {
			t.Helper()
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("the broker created %v in a host directory through the jail's link", entries)
			}
		}
	}},
}

func credsNow(t *testing.T) time.Time {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	saved := nowFunc
	nowFunc = func() int64 { return now.UnixMilli() }
	t.Cleanup(func() { nowFunc = saved })
	return now
}

// THE READS: the cached-token answer (`cached`), the refresh path's cache hit (`refresh`), the
// self-check's grading and the log line. Through a link each answered from the host file: the
// first two served its access token to the jail.
func TestTheBrokerNeverReadsCredsThroughALink(t *testing.T) {
	for _, shape := range credsLinkShapes {
		t.Run(shape.name, func(t *testing.T) {
			now := credsNow(t)
			credsPath := filepath.Join(t.TempDir(), ".credentials.json")
			verify := shape.plant(t, credsPath, now)

			if got := CachedTokens(credsPath); got != nil {
				t.Errorf("CachedTokens served the file behind the jail's link: %v", dumps(got))
			}
			if got := DoRefresh(credsPath); strings.Contains(dumps(got), hostAccessToken) {
				t.Errorf("DoRefresh served the file behind the jail's link: %s", dumps(got))
			}
			for _, l := range gradeSharedCreds(credsPath, now) {
				if l.grade == "OK" {
					t.Errorf("the self-check graded the file behind the jail's link: %+v", l)
				}
			}
			if got := describeCreds(credsPath); strings.Contains(got, TokenFP(hostAccessToken)) {
				t.Errorf("describeCreds read the file behind the jail's link: %s", got)
			}
			verify(t)
		})
	}
}

// A link at the creds path is REPORTED by the self-check, naming it, rather than graded as
// absent: the file is there, and the user needs to know why the broker will not use it.
func TestTheSelfCheckNamesALinkedCredsFile(t *testing.T) {
	now := credsNow(t)
	credsPath := filepath.Join(t.TempDir(), ".credentials.json")
	host, _ := hostCredsFile(t, now)
	if err := os.Symlink(host, credsPath); err != nil {
		t.Fatal(err)
	}
	lines := gradeSharedCreds(credsPath, now)
	if len(lines) != 1 || lines[0].grade != "FAIL" || !strings.Contains(lines[0].title, "symbolic link") {
		t.Errorf("gradeSharedCreds = %+v, want one FAIL naming the symbolic link", lines)
	}
}

// THE WRITE: a refresh's WriteTokens replaces a link at the creds path with a regular file,
// never writing through it.
func TestWriteTokensNeverWritesThroughALink(t *testing.T) {
	for _, shape := range credsLinkShapes {
		t.Run(shape.name, func(t *testing.T) {
			now := credsNow(t)
			credsPath := filepath.Join(t.TempDir(), ".credentials.json")
			verify := shape.plant(t, credsPath, now)

			oauth := jsonx.NewOrderedMap()
			oauth.Set("accessToken", "fresh")
			if err := WriteTokens(credsPath, oauth); err != nil {
				t.Fatal(err)
			}

			verify(t)
			if fi, err := os.Lstat(credsPath); err != nil || !fi.Mode().IsRegular() {
				t.Errorf("the creds path is not a regular file after the write (%v, %v)", fi, err)
			}
		})
	}
}

func dumps(v any) string {
	s, _ := jsonx.DumpsCompact(v)
	return s
}
