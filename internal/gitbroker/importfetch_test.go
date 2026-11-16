package gitbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// TestImportResolverUsesBrokerPolicy drives the host import caller. It proves both that an
// approved fetch imports real objects and that the same-identity-but-different-URL and
// missing-read cases stop before host SSH runs.
func TestImportResolverUsesBrokerPolicy(t *testing.T) {
	for _, tc := range []struct {
		name           string
		url            string
		approved       []string
		current        []string
		approvedGrants []brokerscope.Grant
		userGrants     []brokerscope.Grant
		wantFetch      bool
	}{
		{
			name: "approved remote and broad read grant", url: origin,
			approved: []string{origin}, current: []string{origin},
			approvedGrants: []brokerscope.Grant{readGrant}, userGrants: []brokerscope.Grant{readGrant},
			wantFetch: true,
		},
		{
			name: "missing broad read grant", url: origin,
			approved: []string{origin}, current: []string{origin},
			approvedGrants: []brokerscope.Grant{readGrant}, userGrants: nil,
		},
		{
			name: "missing current remote", url: origin,
			approved: []string{origin}, current: nil,
			approvedGrants: []brokerscope.Grant{readGrant}, userGrants: []brokerscope.Grant{readGrant},
		},
		{
			name: "repository-scoped read is not an import grant", url: origin,
			approved: []string{origin}, current: []string{origin},
			approvedGrants: []brokerscope.Grant{{Kind: "read", Repo: "fixture.test/o/r"}},
			userGrants:     []brokerscope.Grant{{Kind: "read", Repo: "fixture.test/o/r"}},
		},
		{
			name: "same identity different exact URL", url: "alice@fixture.test:o/r.git",
			approved: []string{origin}, current: []string{origin},
			approvedGrants: []brokerscope.Grant{readGrant}, userGrants: []brokerscope.Grant{readGrant},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			setFakeSSHPath(t, f.bin)
			commit := mustGit(t, f.bare, "rev-parse", "refs/heads/main")
			store := filepath.Join(t.TempDir(), "imports.git")
			got, err := FetchImport(context.Background(), ImportFetchRequest{
				Workspace: f.ws, URL: tc.url, Revision: commit, StoreDir: store,
				ApprovedRemotes: tc.approved, CurrentRemotes: tc.current,
				ApprovedGrants: tc.approvedGrants, CurrentGrants: tc.userGrants,
				helperCommand: []string{os.Args[0], "-test.run=^TestHelperProcess$", "--"},
				helperEnv:     []string{"GO_HELPER=1"},
			})
			if tc.wantFetch {
				if err != nil || got.Commit != commit || got.AccessClass != ImportAccessAuthenticated {
					t.Fatalf("FetchImport = (%+v, %v), want commit %s authenticated", got, err, commit)
				}
				if _, err := run(t, store, gitEnv(), "--git-dir", store, "cat-file", "-e", commit+"^{commit}"); err != nil {
					t.Fatalf("import object missing from caller-owned store: %v", err)
				}
				if !f.sshRan() {
					t.Fatal("approved fetch did not use the fake host SSH")
				}
				return
			}
			if err == nil {
				t.Fatalf("FetchImport succeeded with unauthorized authority: %+v", got)
			}
			if f.sshRan() {
				t.Fatalf("denied fetch invoked fake host SSH: %s", f.record)
			}
			if _, statErr := os.Stat(store); !os.IsNotExist(statErr) {
				t.Fatalf("denied fetch left a store at %s (stat %v)", store, statErr)
			}
		})
	}
}

func setFakeSSHPath(t *testing.T, dir string) {
	t.Helper()
	path := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
}

func TestImportResolverPublicHTTPSIsAnonymousWithoutRead(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{public: true, noRead: true})
	store := filepath.Join(t.TempDir(), "imports.git")
	commit := mustGit(t, h.bare, "rev-parse", "refs/heads/main")
	got, err := FetchImport(context.Background(), ImportFetchRequest{
		Workspace: h.ws, URL: h.url, Revision: commit, StoreDir: store,
		ApprovedRemotes: []string{h.url}, CurrentRemotes: []string{h.url},
		helperCommand: []string{os.Args[0], "-test.run=^TestHelperProcess$", "--"},
		helperEnv:     []string{"GO_HELPER=1"}, rootCAs: h.rootCAs,
	})
	if err != nil || got.Commit != commit || got.AccessClass != ImportAccessAnonymous {
		t.Fatalf("public HTTPS FetchImport = (%+v, %v), want anonymous commit %s", got, err, commit)
	}
	if calls := h.credCalls(); len(calls) != 0 {
		t.Fatalf("anonymous import invoked the host credential helper: %v", calls)
	}
	for _, request := range h.srv.requests() {
		if request.AnyAuth {
			t.Fatalf("anonymous import sent authorization to HTTPS server: %+v", request)
		}
	}
}

func TestImportFetchRejectsUnsupportedAndCredentialURLsWithoutWriting(t *testing.T) {
	f := newFixture(t, "")
	for _, url := range []string{"file:///tmp/skills", "https://user:secret@example.test/acme/skills"} {
		store := filepath.Join(t.TempDir(), "store.git")
		if _, err := FetchImport(context.Background(), ImportFetchRequest{
			Workspace: f.ws, URL: url, Revision: strings.Repeat("a", 40), StoreDir: store,
			helperCommand: []string{os.Args[0], "-test.run=^TestHelperProcess$", "--"}, helperEnv: []string{"GO_HELPER=1"},
		}); err == nil {
			t.Errorf("FetchImport(%q) accepted unsupported or credentialed URL", url)
		}
		if _, err := os.Stat(store); !os.IsNotExist(err) {
			t.Errorf("FetchImport(%q) wrote store on validation failure: %v", url, err)
		}
	}
}
