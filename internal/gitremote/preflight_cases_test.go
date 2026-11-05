package gitremote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPreparePushSelectsExplicitRefsAndOptions(t *testing.T) {
	for _, tc := range []struct {
		name, source, ref, old  string
		remoteHTTPS, remoteHTTP bool
		options                 []string
		wantError               string
	}{
		{name: "create", source: "HEAD", ref: "refs/heads/new"},
		{name: "update", source: "HEAD", ref: "refs/heads/old", old: "base"},
		{name: "delete", source: "", ref: "refs/heads/old", old: "base"},
		{name: "force", source: "+HEAD", ref: "refs/heads/old", old: "other"},
		{name: "non-fast-forward-rejected", source: "HEAD", ref: "refs/heads/old", old: "other", wantError: "dry-run failed"},
		{name: "tag", source: "refs/tags/v1", ref: "refs/tags/v1", remoteHTTPS: true},
		{name: "http-helper-only", source: "HEAD", ref: "refs/heads/new", remoteHTTP: true},
		{name: "up-to-date", source: "HEAD", ref: "refs/heads/old", old: "head", wantError: "up-to-date"},
		{name: "wildcard", source: "refs/heads/*", ref: "refs/heads/*", wantError: "refspec"},
		{name: "unresolved", source: "missing", ref: "refs/heads/new", wantError: "local source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work, bare, remote, endpoint, shimDir, packets := gitPreflightFixture(t)
			t.Setenv("YOLO_TEST_GIT_HELPER", "1")
			t.Setenv(EndpointEnv, endpoint)
			t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			git := func(dir string, args ...string) string {
				t.Helper()
				c := exec.Command("git", args...)
				c.Dir = dir
				b, e := c.CombinedOutput()
				if e != nil {
					t.Fatalf("git %v: %v: %s", args, e, b)
				}
				return strings.TrimSpace(string(b))
			}
			head := git(work, "rev-parse", "HEAD")
			wantOld := strings.Repeat("0", 40)
			wantNew := head
			if tc.source == "refs/tags/v1" {
				git(work, "tag", "-a", "-m", "fixture tag", "v1")
				wantNew = git(work, "rev-parse", "refs/tags/v1^{object}")
			}
			if tc.old != "" {
				id := head
				if tc.old == "other" {
					// Distinct commit without a parent, making + a genuinely forced update.
					tree := git(work, "rev-parse", "HEAD^{tree}")
					c := exec.Command("git", "commit-tree", tree)
					c.Dir = work
					c.Stdin = strings.NewReader("other\n")
					b, e := c.CombinedOutput()
					if e != nil {
						t.Fatal(e, string(b))
					}
					id = strings.TrimSpace(string(b))
				}
				wantOld = id
				git(work, "--git-dir="+bare, "fetch", work, id)
				git(work, "--git-dir="+bare, "update-ref", "refs/heads/old", id)
				if tc.old == "base" {
					if e := os.WriteFile(filepath.Join(work, "file"), []byte("second"), 0600); e != nil {
						t.Fatal(e)
					}
					git(work, "add", "file")
					git(work, "commit", "-qm", "second")
					wantNew = git(work, "rev-parse", "HEAD")
				}
			}
			if tc.source == "" {
				wantNew = strings.Repeat("0", 40)
			}
			if tc.remoteHTTPS {
				remote = "yolo::https://example.invalid/o/r"
			} else if tc.remoteHTTP {
				remote = "yolo::http://example.invalid/o/r"
			}
			spec := tc.source + ":" + tc.ref
			args := append([]string{}, tc.options...)
			args = append(args, spec)
			got, err := PreparePush(remote, args, work)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want %s, got %+v %v", tc.wantError, got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Commands) != 1 || got.Commands[0].Ref != tc.ref || !got.OldOIDsUntrusted {
				t.Fatalf("candidate %+v", got)
			}
			if !candidateOIDsMatch(got.Commands[0].Old, got.Commands[0].New, wantOld, wantNew) {
				t.Fatalf("candidate OIDs old=%s new=%s, want old=%s new=%s", got.Commands[0].Old, got.Commands[0].New, wantOld, wantNew)
			}
			if gotPacket := <-packets; string(gotPacket) != "0000" {
				t.Fatalf("receive-pack dry run sent %q, want flush only", gotPacket)
			}
			<-packets // upload-pack advertisement used for untrusted old ID
		})
	}
}

func candidateOIDsMatch(old, new, wantOld, wantNew string) bool {
	return old == wantOld && new == wantNew
}

func TestPreflightOIDAssertionsRejectWrongNonzeroIDs(t *testing.T) {
	wantOld := strings.Repeat("1", 40)
	wantNew := strings.Repeat("2", 40)
	if candidateOIDsMatch(strings.Repeat("3", 40), wantNew, wantOld, wantNew) {
		t.Fatal("OID assertion accepted a wrong nonzero old ID")
	}
	if candidateOIDsMatch(wantOld, strings.Repeat("4", 40), wantOld, wantNew) {
		t.Fatal("OID assertion accepted a wrong nonzero new ID")
	}
}

func TestPreparePushRefusesImplicitSelectionAndNonHelperRemotes(t *testing.T) {
	for _, tc := range []struct {
		remote string
		args   []string
		reason string
	}{
		{"ssh://example.invalid/o/r", []string{"HEAD:refs/heads/main"}, "yolo git helper"},
		{"yolo::ssh://example.invalid/o/r", nil, "no explicit refspec"},
		{"yolo::ssh://example.invalid/o/r", []string{"--tags"}, "unsupported push flag"},
		{"yolo::ssh://example.invalid/o/r", []string{"HEAD"}, "refspec"},
		{"yolo::ssh://example.invalid/o/r", []string{"refs/heads/*:refs/heads/*"}, "refspec"},
	} {
		_, err := PreparePush(tc.remote, tc.args, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%q %v: got %v, want %s", tc.remote, tc.args, err, tc.reason)
		}
	}
}

func TestPreparePushRefusesURLRewritesBeforeTransport(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        string
		value      string
		extraKey   string
		extraValue string
		include    bool
	}{
		{
			name:       "command-scope pushInsteadOf uses longest prefix",
			key:        "url.yolo::ssh://example.invalid/.pushInsteadOf",
			value:      "yolo::ssh://example.invalid/",
			extraKey:   "url.file:///outside/long.pushInsteadOf",
			extraValue: "yolo::ssh://example.invalid/o/r",
		},
		{
			name:  "command-scope insteadOf",
			key:   "url.file:///outside/long.insteadOf",
			value: "yolo::ssh://example.invalid/o/r",
		},
		{
			name:    "included config insteadOf",
			key:     "url.file:///outside/include.insteadOf",
			value:   "yolo::ssh://example.invalid/o/r",
			include: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work, _, remote, endpoint, shimDir, packets := gitPreflightFixture(t)
			t.Setenv("YOLO_TEST_GIT_HELPER", "1")
			t.Setenv(EndpointEnv, endpoint)
			t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("GIT_ALLOW_PROTOCOL", "yolo")
			t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if tc.include {
				root := filepath.Dir(work)
				included := filepath.Join(root, "url-rewrites.inc")
				global := filepath.Join(root, "global.gitconfig")
				if err := os.WriteFile(included, []byte("[url \"file:///outside/include\"]\n\tinsteadOf = "+tc.value+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(global, []byte("[include]\n\tpath = "+included+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GIT_CONFIG_GLOBAL", global)
			} else {
				count := 1
				if tc.extraKey != "" {
					count = 2
				}
				t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(count))
				t.Setenv("GIT_CONFIG_KEY_0", tc.key)
				t.Setenv("GIT_CONFIG_VALUE_0", tc.value)
				if count == 2 {
					t.Setenv("GIT_CONFIG_KEY_1", tc.extraKey)
					t.Setenv("GIT_CONFIG_VALUE_1", tc.extraValue)
				}
			}
			c := exec.Command("git", "remote", "add", "route-check", remote)
			c.Dir = work
			if b, e := c.CombinedOutput(); e != nil {
				t.Fatal(e, string(b))
			}
			c = exec.Command("git", "remote", "get-url", "--push", "route-check")
			c.Dir = work
			b, e := c.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(b))
			}
			if strings.TrimSpace(string(b)) == remote {
				t.Fatalf("fixture's Git rewrite did not change the effective push route: %q", b)
			}
			_, err := PreparePush(remote, []string{"HEAD:refs/heads/new"}, work)
			if err == nil || !strings.Contains(err.Error(), "URL rewrite") {
				t.Fatalf("rewritten helper destination was not refused before transport: %v", err)
			}
			if len(packets) != 0 {
				t.Fatalf("helper/upstream transport ran despite route refusal: %d service requests", len(packets))
			}
		})
	}
}

func TestPreparePushIgnoresNonmatchingURLRewrite(t *testing.T) {
	work, _, _, endpoint, shimDir, packets := gitPreflightFixture(t)
	remote := "yolo::https://example.invalid/o/r"
	t.Setenv("YOLO_TEST_GIT_HELPER", "1")
	t.Setenv(EndpointEnv, endpoint)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.yolo::https://broker.invalid/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := PreparePush(remote, []string{"HEAD:refs/heads/new"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commands) != 1 || got.Commands[0].Ref != "refs/heads/new" {
		t.Fatalf("nonmatching rewrite changed the helper candidate: %+v", got)
	}
	if gotPacket := <-packets; string(gotPacket) != "0000" {
		t.Fatalf("receive-pack dry run sent %q", gotPacket)
	}
	<-packets
}

func TestPreparePushEffectiveOptions(t *testing.T) {
	work, bare, remote, endpoint, shimDir, packets := gitPreflightFixture(t)
	t.Setenv("YOLO_TEST_GIT_HELPER", "1")
	t.Setenv(EndpointEnv, endpoint)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := exec.Command("git", "config", "--add", "push.pushOption", "first")
	c.Dir = work
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	c = exec.Command("git", "config", "--add", "push.pushOption", "second")
	c.Dir = work
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	c = exec.Command("git", "--git-dir="+bare, "config", "receive.advertisePushOptions", "true")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	got, err := PreparePush(remote, []string{"HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.PushOptions, ",") != "first,second" {
		t.Fatalf("config option order %+v", got)
	}
	<-packets
	<-packets
	c = exec.Command("git", "config", "--add", "push.pushOption", "")
	c.Dir = work
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	c = exec.Command("git", "config", "--add", "push.pushOption", "after-reset")
	c.Dir = work
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	got, err = PreparePush(remote, []string{"HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.PushOptions, ",") != "after-reset" {
		t.Fatalf("config reset order %+v", got)
	}
	<-packets
	<-packets
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "push.pushOption")
	t.Setenv("GIT_CONFIG_VALUE_0", "")
	got, err = PreparePush(remote, []string{"HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PushOptions) != 0 {
		t.Fatalf("inherited empty config reset left options %+v", got.PushOptions)
	}
	<-packets
	<-packets
	got, err = PreparePush(remote, []string{"-o", "cli-first", "--push-option=cli-second", "HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.PushOptions, ",") != "cli-first,cli-second" {
		t.Fatalf("CLI override order %+v", got)
	}
	<-packets
	<-packets
	got, err = PreparePush(remote, []string{"--push-option=", "HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PushOptions) != 1 || got.PushOptions[0] != "" {
		t.Fatalf("empty CLI option was not preserved as a wire option: %+v", got.PushOptions)
	}
	<-packets
	<-packets
}
