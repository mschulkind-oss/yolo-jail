package entrypoint

// updaterefusal_test.go RUNS the launchers' no-silent-fallback ruling (docs/design/program-delivery.md
// PD-NF1..PD-NF6) through the generated launchers themselves: when a launcher found a newer version
// of its program and installing it failed, the command STOPS with the one refusal shape (ERROR, the
// fact, Repair:, Start anyway:) and exits UpdateRefusedStatus without running the program. Only the
// user's YOLO_USE_INSTALLED or an agent_updates hold runs the older version. An upstream that could
// not be reached at all is not a failure: the installed version runs, and the launcher says it could
// not check.
//
// Every cell drives fakes (npm, a vendor program, local installer hosts); nothing reaches a network
// and no agent is started (AGENTS.md, "No agent tests").

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// warmNpmProbe is an npm probe holding tool 1.0.0, installed, with its update due.
func warmNpmProbe(t *testing.T) *npmProbe {
	t.Helper()
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.newJailStart(t)
	p.truncateLog(t)
	return p
}

// THE NPM CASE, whole: the registry names 9.9.9, its install fails, and the launch refuses with the
// exact refusal — the hatch line rendered by patchrecovery — runs nothing, and leaves the update
// due, so the next invocation retries it instead of running 1.0.0 quietly for an hour.
func TestANewerNpmVersionThatFailsToInstallRefuses(t *testing.T) {
	p := warmNpmProbe(t)
	log, out, rc := p.runStatus(t, "tool", "tool", "FAKE_LATEST=9.9.9", "FAKE_INSTALL_FAIL=1")
	if rc != UpdateRefusedStatus {
		t.Fatalf("rc=%d, want %d:\n%s", rc, UpdateRefusedStatus, out)
	}
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("the newer version was never attempted:\n%s", strings.Join(log, "\n"))
	}
	want := "yolo: ERROR: tool 1.0.0 → 9.9.9: installing the newer version failed\n" +
		"  npm install -g tool@latest exited with an error (its output is above).\n" +
		"\n" +
		"Repair:\n" +
		"  fix what stopped it (its output is above), then run tool again, which retries the update:\n" +
		"    tool\n" +
		"\nStart anyway:\n" + useInstalledLines("tool", "tool", "1.0.0")
	if !strings.HasSuffix(out, want) {
		t.Errorf("the refusal does not end with\n%s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, "RAN") {
		t.Errorf("a refused launch ran the program:\n%s", out)
	}
	if m := p.markers(t, "tool"); len(m) != 0 {
		t.Errorf("a refused update left its check marker %v: the next invocation would run 1.0.0 without retrying", m)
	}
	p.truncateLog(t)
	if log, _, rc := p.runStatus(t, "tool", "tool", "FAKE_LATEST=9.9.9", "FAKE_INSTALL_FAIL=1"); rc != UpdateRefusedStatus ||
		!hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Errorf("the next invocation must retry and refuse again (rc=%d):\n%s", rc, strings.Join(log, "\n"))
	}
}

// THE HATCH: YOLO_USE_INSTALLED names the program by its bin or its pack, or is "1", and the
// installed version runs, saying why; a name that matches neither still refuses.
func TestUseInstalledRunsTheInstalledVersion(t *testing.T) {
	for _, tc := range []struct {
		value string
		runs  bool
	}{{"tool", true}, {"probe", true}, {"1", true}, {" other , tool ", true}, {"other", false}, {"tool-x", false}} {
		t.Run(tc.value, func(t *testing.T) {
			p := warmNpmProbe(t)
			_, out, rc := p.runStatus(t, "tool", "tool", "FAKE_LATEST=9.9.9", "FAKE_INSTALL_FAIL=1",
				"YOLO_USE_INSTALLED="+tc.value)
			if !tc.runs {
				if rc != UpdateRefusedStatus || strings.Contains(out, "RAN") {
					t.Errorf("YOLO_USE_INSTALLED=%q covers no name of this program, yet rc=%d:\n%s", tc.value, rc, out)
				}
				return
			}
			if rc != 0 || !strings.Contains(out, "RAN") {
				t.Fatalf("YOLO_USE_INSTALLED=%q must run the installed version, rc=%d:\n%s", tc.value, rc, out)
			}
			if !strings.Contains(out, "⚠ tool 1.0.0 → 9.9.9: installing the newer version failed — running the installed version 1.0.0, as YOLO_USE_INSTALLED asks.") {
				t.Errorf("the hatch run must say what failed and why the older version runs:\n%s", out)
			}
			if strings.Contains(out, "yolo: ERROR") {
				t.Errorf("a hatch run is not a refusal:\n%s", out)
			}
		})
	}
}

// NOT A FAILURE: a registry that does not answer says nothing is newer. The installed version runs,
// the launch says it could not ask, and it stamps, so the next invocation inside the hour asks
// nothing.
func TestAnUnreachableRegistryRunsTheInstalledNpmVersion(t *testing.T) {
	p := warmNpmProbe(t)
	log, out, rc := p.runStatus(t, "tool", "tool", "FAKE_VIEW_FAIL=1")
	if rc != 0 || !strings.Contains(out, "RAN") {
		t.Fatalf("an unreachable registry must run the installed version, rc=%d:\n%s", rc, out)
	}
	if !strings.Contains(out, "tool: could not ask the npm registry for a newer version — running the installed 1.0.0.") {
		t.Errorf("the launch must say it could not check:\n%s", out)
	}
	if hasArgv(log, "install") {
		t.Errorf("nothing was known to be newer, yet an install ran:\n%s", strings.Join(log, "\n"))
	}
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool", "FAKE_VIEW_FAIL=1"); hasArgv(log, "view") {
		t.Errorf("a could-not-check must stamp, so the next invocation asks nothing:\n%s", strings.Join(log, "\n"))
	}
}

// PD-NF6: a pin whose DECLARATION moved is a known target, so a failed install refuses even with
// no registry at all; the user's agent_updates hold runs the installed version instead, saying so.
func TestAMovedPinThatFailsToInstallRefusesOfflineAndAHoldRunsIt(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3")
	_, out, rc := p.runStatus(t, "tool", "tool@1.3.0", "FAKE_INSTALL_FAIL=1", "FAKE_VIEW_FAIL=1", registryDownEnv)
	if rc != UpdateRefusedStatus || strings.Contains(out, "RAN") {
		t.Fatalf("a moved pin that did not install must refuse, offline included (rc=%d):\n%s", rc, out)
	}
	for _, want := range []string{"yolo: ERROR: tool 1.2.3 → tool@1.3.0: installing the version its pack declares failed",
		"YOLO_USE_INSTALLED=tool tool", "run the installed 1.2.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, out)
		}
	}

	p.updates = false
	_, out, rc = p.runStatus(t, "tool", "tool@1.3.0", "FAKE_INSTALL_FAIL=1")
	if rc != 0 || !strings.Contains(out, "RAN") || !strings.Contains(out, "as this jail's agent_updates holds it") {
		t.Errorf("an agent_updates hold must run the installed version, saying so (rc=%d):\n%s", rc, out)
	}
}

// PD-NF3, a vendor's update verb: it has no version query, so its failure is judged by its upstream.
// The installer host answers: refused, and the hatch by pack name runs the installed version. The
// host is gone: could not check, and the installed version runs, saying so.
func TestANativeVerbThatFailsIsJudgedByItsUpstream(t *testing.T) {
	failing := func(t *testing.T, p *updateProbe) {
		t.Helper()
		body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
			"if [ \"${1:-}\" = update ]; then exit 7; fi\n"
		if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("upstream answers", func(t *testing.T) {
		p := newUpdateProbeAt(t, []string{"update"}, true, true, answeringHost(t))
		failing(t, p)
		out, rc := p.run(t)
		if rc != UpdateRefusedStatus {
			t.Fatalf("rc=%d, want %d:\n%s", rc, UpdateRefusedStatus, out)
		}
		for _, want := range []string{"yolo: ERROR: probetool: its update (probetool update) failed (status 7)\n",
			"\nRepair:\n", "\nStart anyway:\n", useInstalledLines("probetool", "probetool", "version")} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal lacks %q:\n%s", want, out)
			}
		}
		if got := p.argvLog(t); len(got) != 1 || got[0] != "RAN:update" {
			t.Errorf("a refused launch must run the update and nothing else, got %v", got)
		}
		out, rc = p.run(t, "YOLO_USE_INSTALLED=probe")
		if rc != 0 || !strings.HasSuffix(strings.Join(p.argvLog(t), " "), "RAN:update RAN:") {
			t.Errorf("the hatch naming the pack must run the installed version (rc=%d), got %v:\n%s", rc, p.argvLog(t), out)
		}
	})
	t.Run("upstream silent", func(t *testing.T) {
		p := newUpdateProbeAt(t, []string{"update"}, true, true, silentHost(t))
		failing(t, p)
		out, rc := p.run(t)
		if rc != 0 || !strings.Contains(out, "could not be reached to check for a newer version — running what is installed") {
			t.Errorf("an unreachable upstream must run the installed version, saying so (rc=%d):\n%s", rc, out)
		}
	})
}

// THE PROBE'S CLASSIFICATION is the host floor's (hostfloor.probeUpstreamHEAD): a server error is
// an upstream that could not be checked, so a failed update verb runs the installed version; any
// other HTTP status, a 404 or a 405 for HEAD, is an upstream that answered, and the launch refuses.
func TestTheJailsUpstreamProbeClassifiesAsTheHostDoes(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	for _, tc := range []struct {
		status    int
		reachable bool
	}{{404, true}, {405, true}, {500, false}, {503, false}} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)
			p := newUpdateProbeAt(t, []string{"update"}, true, true, srv.URL+"/install.sh")
			body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
				"if [ \"${1:-}\" = update ]; then exit 7; fi\n"
			if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			out, rc := p.run(t)
			if tc.reachable {
				if rc != UpdateRefusedStatus {
					t.Errorf("an upstream answering %d answered: the launch must refuse, rc=%d:\n%s", tc.status, rc, out)
				}
				return
			}
			if rc != 0 || !strings.Contains(out, "could not be reached to check for a newer version") {
				t.Errorf("an upstream answering %d could not be checked: the installed version must run (rc=%d):\n%s", tc.status, rc, out)
			}
		})
	}
}

// A vendor INSTALLER re-run (no verb declared): one that was fetched and failed refuses, its host
// having answered; one that could not be downloaded could not check, and the installed version runs.
func TestAnInstallerReRunIsJudgedByItsDownload(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	t.Run("fetched and failed", func(t *testing.T) {
		url := installerURL(t, "#!/bin/bash\necho 'no such release' >&2\nexit 4\n")
		p := newUpdateProbeAt(t, nil, true, true, url)
		out, rc := p.run(t)
		if rc != UpdateRefusedStatus || strings.Contains(strings.Join(p.argvLog(t), " "), "RAN:") {
			t.Fatalf("a failed installer re-run must refuse (rc=%d), ran %v:\n%s", rc, p.argvLog(t), out)
		}
		for _, want := range []string{"yolo: ERROR: probetool: re-running its installer to update it failed\n",
			"  its installer (" + url + ") exited with status 4 (its output is above).\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("could not download", func(t *testing.T) {
		p := newUpdateProbeAt(t, nil, true, true, silentHost(t))
		out, rc := p.run(t)
		if rc != 0 || !strings.Contains(out, "could not download its installer to check for a newer version — running the installed version") {
			t.Errorf("an installer that could not be fetched must run the installed version (rc=%d):\n%s", rc, out)
		}
		if got := p.argvLog(t); len(got) != 1 || got[0] != "RAN:" {
			t.Errorf("the installed version must run, got %v", got)
		}
	})
}

// A TIMEOUT is a failure: the act ran out its bound while its upstream answers, so the launch
// refuses, saying it timed out.
func TestATimedOutUpdateWhoseUpstreamAnswersRefuses(t *testing.T) {
	p := newBoundProbe(t, boundProbeOpts{npm: true, verb: []string{"install"}, behave: sleeps, timeout: 1, grace: 1})
	cmd := exec.Command(p.script)
	cmd.Dir = p.home
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + fakeRegistryDir(t) + ":" + pathFor(t, true)}
	out, err := cmd.CombinedOutput()
	if code := exitCode(err); code != UpdateRefusedStatus {
		t.Fatalf("rc=%d, want %d:\n%s", code, UpdateRefusedStatus, out)
	}
	if !strings.Contains(string(out), "yolo: ERROR: probetool: its update (probetool install) timed out after 1s") ||
		strings.Contains(string(out), "AGENT_RAN") {
		t.Errorf("a timed-out update must refuse, saying it timed out, and run nothing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(p.home, ".npm-global", ".yolo-update.lock")); !os.IsNotExist(err) {
		t.Errorf("a refused update must release the install-prefix lock (err=%v)", err)
	}
}

// THE PRE-LAUNCH REFRESH is an update act with no version query (PD-NF3): failed with its registry
// answering, the launch refuses, the lock released, the stamp removed so the next launch retries;
// the hatch runs the program, stamped.
func TestAFailedPrelaunchRefreshWhoseRegistryAnswersRefuses(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	stdout, stderr, err := p.runStatus(t, "", "FAKE_REFRESH_RC=3")
	if code := exitCode(err); code != UpdateRefusedStatus {
		t.Fatalf("rc=%d, want %d:\n%s", code, UpdateRefusedStatus, stderr)
	}
	for _, want := range []string{"yolo: ERROR: tool: the pre-launch refresh (update --extensions) failed (status 3)\n",
		"\nRepair:\n", "\nStart anyway:\n", useInstalledLines("tool", "tool", "version")} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "RAN") || countLine(p.logLines(t), "LAUNCH:") != 0 {
		t.Errorf("a refused launch ran the program: %v", p.logLines(t))
	}
	if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
		t.Errorf("a refused refresh must release its lock (err=%v)", err)
	}
	if _, err := os.Stat(p.stampPath()); !os.IsNotExist(err) {
		t.Errorf("a refused refresh left its stamp (err=%v): the next launch would not retry it", err)
	}

	stdout, stderr = p.run(t, "", "FAKE_REFRESH_RC=3", "YOLO_USE_INSTALLED=tool")
	assertProgramLaunched(t, p.logLines(t), stdout)
	if !strings.Contains(stderr, "as YOLO_USE_INSTALLED asks") {
		t.Errorf("the hatch run must say why the program runs:\n%s", stderr)
	}
	if _, err := os.Stat(p.stampPath()); err != nil {
		t.Errorf("a hatch run must keep the stamp: %v", err)
	}
}

// A CTRL-C at the refresh is the user's own hatch at that moment (PD-NF4) — pinned under a terminal
// in updatebound_linux_test.go — and a BUSY lock checked nothing (PD-NF5): both run the program.
func TestABusyRefreshLockIsNotAFailure(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	if err := os.MkdirAll(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := p.run(t, "", "FAKE_REFRESH_RC=3")
	assertProgramLaunched(t, p.logLines(t), stdout)
	if !strings.Contains(stderr, "another refresh holds") || strings.Contains(stderr, "yolo: ERROR") {
		t.Errorf("a held lock must run what is installed, saying so:\n%s", stderr)
	}
}

// AN MCP SERVER'S REFUSAL is said by `yolo internal refresh-servers`, which exits
// UpdateRefusedStatus; every launcher template passes that on instead of exec'ing its agent, and
// names itself (--bin) so the refusal can say what to run again. Any other status is advisory.
func TestALauncherPassesAServerRefusalOn(t *testing.T) {
	for _, tpl := range []string{"npm", "native", "source"} {
		for _, rc := range []string{"75", "1"} {
			t.Run(tpl+"/"+rc, func(t *testing.T) {
				home := t.TempDir()
				fakeBin := filepath.Join(home, "fakebin")
				if err := os.MkdirAll(fakeBin, 0o755); err != nil {
					t.Fatal(err)
				}
				yoloLog := filepath.Join(home, "yolo.log")
				if tpl != "source" {
					argvLogger(t, fakeBin, "yolo", yoloLog, `if [ "${2:-}" = refresh-servers ]; then exit `+rc+`; fi`)
				}
				stamps := filepath.Join(home, "stamps")
				receipts := filepath.Join(home, "ws", ".yolo", "receipts.jsonl")
				servers := launcherServers{npm: "chrome-devtools-mcp"}
				var body, realBin, sourceKeyDir string
				switch tpl {
				case "npm":
					realBin = filepath.Join(home, ".npm-global", "bin", "tool")
					body = npmAgentLauncher("probe", &packdecl.Install{Kind: "npm", Bin: "tool", Package: "tool"},
						stamps, receipts, true, servers, nil)
				case "native":
					realBin = filepath.Join(home, ".local", "bin", "tool")
					body = nativeAgentLauncher("probe", &packdecl.Install{Kind: "native", Bin: "tool",
						InstallerURL: "https://example.invalid/never-fetched.sh"}, stamps, receipts, "", true, servers, nil)
				case "source":
					realBin = filepath.Join(home, ".local", "bin", "tool")
					sourceKeyDir = filepath.Join(home, ".local", "state", "yolo", "fork-keys")
					writeTestFile(t, filepath.Join(sourceKeyDir, "tool"), "old-key\n")
				}
				seedInstalled(t, filepath.Dir(realBin), "tool")
				seedFreshStamp(t, stamps, "tool")
				if tpl == "source" {
					captures, key := admitForkLauncherCapture(t, home, "tool", ".local/bin/tool", realBin)
					body = strings.Join(sourceAgentLauncherSegments(&packdecl.Install{Kind: packdecl.InstallKindSource,
						Bin: "tool", ForkedBy: "forkpack", Produces: []string{".local/bin/tool"}},
						ForkDelivery{Key: key}, stamps, sourceKeyDir, receipts, captures, true, servers, nil), "")
					realYolo := filepath.Join(forkMaterializerPath(t), "yolo")
					argvLogger(t, fakeBin, "yolo", yoloLog, `
if [ "${2:-}" = capture-materialize ]; then exec `+shellSingleQuote(realYolo)+` "$@"; fi
if [ "${2:-}" = refresh-servers ]; then exit `+rc+`; fi`)
				}
				out, code := runLauncher(t, home, "launch-tool", body, fakeBin)
				if !hasExactArg(logLines(t, yoloLog), "--bin=tool") {
					t.Errorf("the refresh call must name its agent:\n%s", strings.Join(logLines(t, yoloLog), "\n"))
				}
				if rc == "75" {
					if code != UpdateRefusedStatus || strings.Contains(out, "LAUNCHED") {
						t.Errorf("a server refusal must stop the launcher with %d, got %d:\n%s", UpdateRefusedStatus, code, out)
					}
					return
				}
				if code != 0 || !strings.Contains(out, "LAUNCHED") {
					t.Errorf("an advisory server failure must still launch (rc=%d):\n%s", code, out)
				}
			})
		}
	}
}

// A HATCHED REFUSAL LEAVES NOTHING BEHIND: a failed update the hatch ran past must not hand its head
// to a later act in the same run, so a pre-launch refresh that then could not check says only that,
// rather than repeating the update's failure as its own.
func TestAHatchedRefusalLeavesNoHeadForTheNextAct(t *testing.T) {
	fn := strings.NewReplacer(useInstalledSplices("tool", "probe")...).Replace(updateBoundShellFn)
	script := "BIN=tool\nUPDATE_TIMEOUT=60\n" + fn + `
_YOLO_FAIL_HEAD="tool 1.0.0 → 9.9.9: the first act failed"
_YOLO_FAIL_FACT="its output is above."
_YOLO_FAIL_VER=1.0.0
_yolo_refuse_failed ""
_yolo_failed_act 3 "the second act" false
_yolo_refuse_failed ""
echo "END:[$_YOLO_FAIL_HEAD][$_YOLO_FAIL_FACT][$_YOLO_FAIL_VER]"
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "YOLO_USE_INSTALLED=tool")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the hatched run must not refuse (%v):\n%s", err, out)
	}
	if n := strings.Count(string(out), "the first act failed"); n != 1 {
		t.Errorf("the first act's failure must be said once, said %d times:\n%s", n, out)
	}
	if !strings.Contains(string(out), "tool: the second act failed (status 3), and its upstream could not be reached") ||
		!strings.Contains(string(out), "END:[][][]") {
		t.Errorf("the second act must speak for itself and leave nothing set:\n%s", out)
	}
}
