package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestLaunchAsksNothingForAVacuousScope drives the launch's own gate (checkConfigChanges, which
// reads the scope through brokeredScopeCheck) with the legacy user read grant for every
// workspace, for a broker on any host (git's shape) and one on a named host (github's): a
// workspace with no remote the broker could serve asks nothing, records nothing for the source,
// and hands the broker an approved empty scope with no grant. The same launch with a real remote
// still refuses without a terminal, which is what fails if the production call is removed.
func TestLaunchAsksNothingForAVacuousScope(t *testing.T) {
	for _, host := range []string{"*", "github.com"} {
		for _, ws := range []string{"non-Git", "no remotes", "one remote"} {
			t.Run(host+"/"+ws, func(t *testing.T) {
				o, buf, _ := brokeredFixtureOn(t, true, host)
				switch ws {
				case "non-Git":
					if err := os.RemoveAll(filepath.Join(o.Workspace, ".git")); err != nil {
						t.Fatal(err)
					}
				case "no remotes":
					if err := os.WriteFile(filepath.Join(o.Workspace, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				ok := o.checkConfigChanges(gbOn(), "podman")
				rec := map[string][]string{}
				if data, err := os.ReadFile(config.ApprovalScopePath(o.Workspace)); err == nil {
					if err := json.Unmarshal(data, &rec); err != nil {
						t.Fatal(err)
					}
				}
				if ws == "one remote" {
					// The positive control: a concrete scope reaches the gate, which a launch with
					// no terminal refuses.
					if ok || !strings.Contains(buf.String(), "gb repository scope") {
						t.Fatalf("a real remote did not reach the gate:\n%s", buf.String())
					}
					return
				}
				if !ok {
					t.Fatalf("a vacuous scope stopped the launch:\n%s", buf.String())
				}
				if strings.Contains(buf.String(), "repository scope") || strings.Contains(buf.String(), "grant read") {
					t.Fatalf("a vacuous scope was put to the reader:\n%s", buf.String())
				}
				if v, has := rec["gbsrc"]; has {
					t.Fatalf("a vacuous scope recorded its scope: %v", v)
				}
				if v, has := rec["gbsrc.grants"]; has {
					t.Fatalf("a vacuous scope recorded grants: %v", v)
				}
				approved, has := o.approvedScopes["gb"]
				if !has || len(approved) != 0 {
					t.Fatalf("the launch did not approve an empty scope for the broker: %v (present=%v)", approved, has)
				}
				if g := o.approvedGrants["gb"]; len(g) != 0 {
					t.Fatalf("a vacuous scope handed its broker grants: %v", g)
				}
			})
		}
	}
}

// vacuousWorkspace makes the fixture's workspace one with no remote its broker could serve:
// "non-Git" removes the checkout, "no remotes" leaves a checkout whose config names none.
func vacuousWorkspace(t *testing.T, ws, shape string) {
	t.Helper()
	switch shape {
	case "non-Git":
		if err := os.RemoveAll(filepath.Join(ws, ".git")); err != nil {
			t.Fatal(err)
		}
	case "no remotes":
		if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown workspace shape %q", shape)
	}
}

// writeRawUserConfig replaces the user config with exactly name's text, with no read grant added
// (writeUserConfigOf always adds one).
func writeRawUserConfig(t *testing.T, name, text string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// scopeLaunchLine drives a launch with no terminal through its own gate (checkConfigChanges, which
// records what it approved through recordApprovedScopes) and then through writeScopeFiles' own
// line for the brokers that start, and returns everything it printed.
func scopeLaunchLine(t *testing.T, o *Options, buf *strings.Builder, merged *jsonx.OrderedMap) string {
	t.Helper()
	if !o.checkConfigChanges(merged, "podman") {
		t.Fatalf("a scope with no remote stopped the launch:\n%s", buf.String())
	}
	brokered := o.brokeredToStart("podman", merged)
	if len(brokered) == 0 {
		t.Fatalf("no broker starts:\n%s", buf.String())
	}
	o.writeScopeFiles("yolo-vacuous-line", brokered)
	return buf.String()
}

// addRemote is the next step the line must name.
const addRemote = "`git remote add`"

// TestAHeldReadGrantOverNoRemoteNamesTheMissingRemote is V-G2's launch line (V-G2b). A user who
// holds the read grant, in a workspace with no remote the broker could serve, gets an approved
// empty scope with no grant (TestLaunchAsksNothingForAVacuousScope); the line the launch prints
// for it must name the missing remote and how to add one, never "no read grant applies" with
// advice to add the grant they already hold. Without the grant the old line stands. Both host
// shapes, both kinds of remote-less workspace, through the legacy (JSON) user grant.
func TestAHeldReadGrantOverNoRemoteNamesTheMissingRemote(t *testing.T) {
	for _, host := range []string{"*", "github.com"} {
		for _, ws := range []string{"non-Git", "no remotes"} {
			for _, held := range []bool{true, false} {
				name := host + "/" + ws + "/no grant"
				if held {
					name = host + "/" + ws + "/read grant"
				}
				t.Run(name, func(t *testing.T) {
					o, buf, _ := brokeredFixtureOn(t, true, host)
					if !held {
						writeRawUserConfig(t, "config.jsonc", "{}\n")
					}
					vacuousWorkspace(t, o.Workspace, ws)
					out := scopeLaunchLine(t, o, buf, gbOn())
					if !held {
						if !strings.Contains(out, "gb: grants nothing in this workspace: no read grant applies") {
							t.Fatalf("a launch with no read grant does not say so:\n%s", out)
						}
						if strings.Contains(out, "your read grant applies") {
							t.Fatalf("a launch with no read grant claims one:\n%s", out)
						}
						return
					}
					if strings.Contains(out, "no read grant applies") || strings.Contains(out, "To let every workspace read") {
						t.Fatalf("a held read grant was reported missing:\n%s", out)
					}
					want := "gb: your read grant applies, but this workspace has no remote, so the broker has no " +
						"repository to serve and every call through it is refused; add one with " + addRemote + ", " +
						"and the next launch asks about it"
					if host != "*" {
						want = "gb: your read grant applies, but this workspace has no remote on github.com and no " +
							"`[repos.<name>]` entry, so the broker has no repository to serve and every call " +
							"through it is refused; add a remote with " + addRemote + " (or name the repository with " +
							"`yolo request repos <alias> github.com/<path>`), and the next launch asks about it"
					}
					if !strings.Contains(out, want) {
						t.Fatalf("the launch does not name the missing remote as\n  %s\n%s", want, out)
					}
				})
			}
		}
	}
}

// The same line through the readable producer (scopeCheckFromReadable): a TOML user config whose
// `[allow.github]` puts read in `always` allows the read, and one with read only `on_request`,
// never requested, does not. Grants there are issued per concrete target, so the producer must
// read the allowance off its input to tell the two apart.
func TestAReadableReadAllowanceOverNoRemoteNamesTheMissingRemote(t *testing.T) {
	for _, ws := range []string{"non-Git", "no remotes"} {
		for _, held := range []bool{true, false} {
			name := ws + "/no read allowance"
			if held {
				name = ws + "/read allowed"
			}
			t.Run(name, func(t *testing.T) {
				o, buf, marker := brokeredFixtureBlock(t, true,
					`{"source": "github", "remote_host": "github.com", "read_capability": "read"}`)
				mod := filepath.Join(t.TempDir(), "github-broker")
				if err := os.MkdirAll(mod, 0o755); err != nil {
					t.Fatal(err)
				}
				manifest := `{"name": "github-broker", "transport": "loopback-tls", "lifecycle": "spawned",
				  "host_daemon": {"cmd": ["/bin/cp", "{repository_scope}", "` + marker + `"], "publishes": "socket"},
				  "brokered": {"source": "github", "remote_host": "github.com", "read_capability": "read"}}`
				if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
					t.Fatal(err)
				}
				loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, Pack: "github", HostExecApproved: true}})
				allow := "[allow.github]\non_request = [\"read\"]\n"
				if held {
					allow = "[allow.github]\nalways = [\"read\"]\n"
				}
				writeRawUserConfig(t, paths.UserConfigTOMLName, allow)
				vacuousWorkspace(t, o.Workspace, ws)
				out := scopeLaunchLine(t, o, buf, ghOn())
				if !held {
					if !strings.Contains(out, "github-broker: grants nothing in this workspace: no read grant applies") {
						t.Fatalf("a launch with no read allowance does not say so:\n%s", out)
					}
					return
				}
				if strings.Contains(out, "no read grant applies") {
					t.Fatalf("an allowed read was reported missing:\n%s", out)
				}
				want := "github-broker: your read grant applies, but this workspace has no remote on github.com and no " +
					"`[repos.<name>]` entry, so the broker has no repository to serve and every call through " +
					"it is refused; add a remote with " + addRemote + " (or name the repository with " +
					"`yolo request repos <alias> github.com/<path>`), and the next launch asks about it"
				if !strings.Contains(out, want) {
					t.Fatalf("the launch does not name the missing remote as\n  %s\n%s", want, out)
				}
			})
		}
	}
}
