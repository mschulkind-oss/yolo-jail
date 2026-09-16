package run

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CENSUS IN docs/design/backend-parity.md IS PROSE, AND PROSE CANNOT FAIL.
//
// Two issues eight months apart — #39 (pack SharedDirs never mounted on Apple Container)
// and #44 (the staged pack tree never mounted there, so no agent reaches the jail at all
// on the backend the README recommends for macOS) — were the same shape: a branch in the
// run pipeline that does one thing on podman and something else, or nothing, on another
// backend, with nobody having decided that was correct. Both were found by a human on
// hardware, because NO PUSH-TRIGGERED CI JOB RUNS APPLE CONTAINER: every job that sets
// YOLO_RUNTIME job-wide sets podman (ci.yml, nightly-macos.yml, packs.yml), macos-user.yml
// sets none because that backend starts no container, and the one workflow that does run the
// backend — apple-container.yml — is dispatch-only on a self-hosted Mac that is off much of
// the time, so a push still merges with nothing having exercised it.
//
// So this test makes the census EXECUTABLE, in the shape
// TestThePreflightRunsEveryBootGenerator and TestEveryFlaggedBinGetsACarrier already use:
// the ENUMERATION is read out of the tree, and only the CARVE-OUTS are written down. Every
// line in the run pipeline that branches on the runtime's identity must either carry a
// `parity:` marker saying which disposition it is and why, or be counted in
// parityBacklog below as NOT YET CLASSIFIED. A new branch that is in neither fails this
// test, naming its file, its line, and what to write.
//
// ─── WHAT THIS DOES NOT CATCH. Read this before trusting a green. ───
//
//  1. **A divergence with no branch at all.** #39 was not a wrong `if` — it was an ABSENT
//     mount in appleContainerBaseMounts, and an absent thing has no line to mark. This test
//     cannot see it and never will. That class belongs to the argv-diff tests, which compare
//     the two backends' own output rather than the source text:
//     TestMachineWideMountsReachBothContainerBackends (machinetierparity_test.go),
//     TestNoHostHomeBindSurvivesOnAppleContainer (hosthometier_test.go) and
//     TestNoJailEnvVarNamesAnUnreachableHostPath (hostpathenv_test.go).
//  2. **A branch that is DECLARED and WRONG.** #44's site was declared and wrong at once —
//     assemble.go's `rt == "container"` arm set YOLO_PACK_ROOT to a HOST path, on the belief
//     that Apple Container can read the host filesystem, and a census would have marked that
//     Honored and been satisfied. (The arm now materializes the tree into ws_state and names
//     an in-jail path, marked HonoredBy; the class did not go away with it.) This is
//     backend-parity.md §4's residue 1, and it is why hostpathenv_test.go exists.
//  3. **A fork on something other than the runtime.** `o.IsMacOS`, `runtime.GOOS`, a map
//     keyed by backend, a capability probe. Those are HOST-PLATFORM questions, not backend
//     questions — podman runs on macOS too — and folding them in would bury the real
//     sites in probe noise. A line that mixes them (`if o.IsMacOS || rt == "container"`) is
//     caught by its `rt` half.
//  4. **Anything outside internal/cli/run.** parityScope is one directory on purpose: it is
//     the pipeline that decides what a launch does. `internal/cli/check`, `internal/prune`,
//     `internal/runtime` and `internal/image` branch on the runtime too, almost always to
//     pick which CLI to speak to, and a census over them would be ~40 more Honored cells
//     that teach nobody anything. Widening the scope is a one-line change here plus the
//     backlog rows it uncovers.
//
// In one sentence: this test makes an UNCLASSIFIED branch impossible, not a WRONG one.
const parityScope = "."

// The dispositions, all six of them docs/design/backend-parity.md §3's — the census there is
// per CONFIG KEY, and these are the same vocabulary applied to a code site.
//
// Dropped and NotApplicable are the two this census DROVE INTO §3, which had four: §3 classifies
// a mechanism a user asked for, and every one of those is in one of four states. A code site is a
// smaller thing, and many of them are not answering a user-facing question at all —
// `canNest(rt string) bool { return rt == "podman" }` is a fact about podman-in-podman, not a
// capability Apple Container is missing. Without the extra two those sites would have to be
// spelled Honored, which would make Honored mean two different things.
var parityDispositions = map[string]string{
	// Every backend reaches the same outcome; this branch is only the different spelling
	// (which CLI binary, which flag syntax).
	"Honored": "same outcome, different spelling",
	// The other backend reaches the same outcome by a DIFFERENT mechanism, which the reason
	// must name — §3's warning is that the un-named version of this sentence is what hid #39.
	"HonoredBy": "same outcome, different mechanism (name it)",
	// The other backend does not get this, and the launch says so. The reason should name
	// where it says so.
	"Warned": "absent elsewhere, and the launch says so",
	// The launch refuses rather than degrading.
	"Refused": "the launch refuses and names the key",
	// Absent elsewhere, deliberately, and deliberately NOT warned about — §5.1's rows,
	// whose whole argument is that a warning here would train the reader to skip the ones
	// that matter. §5.1 used to say these "belong in the census as a Warned or HonoredBy
	// cell", which was the one sentence in that document the code could not honor: they are
	// neither, and spelling a silent drop `Warned` would make the census assert a launch
	// line that does not exist. §5.1 now says `Dropped`, and §3 defines it.
	"Dropped": "absent elsewhere, silently, on purpose",
	// The capability question does not arise on the other backends.
	"NotApplicable": "not a capability question",
}

// parityGate matches a line that branches on the RUNTIME'S IDENTITY. One line is one site,
// however many comparisons it holds, because the marker is a trailing comment and a line has
// one of those.
//
// The three shapes are: a comparison of an `rt`-ish variable (bare, or a field like `in.rt`)
// against one of the three runtime names; the same for a variable spelled `runtime`; and a
// membership test against one of paths' runtime lists.
var parityGate = regexp.MustCompile(
	`(?:\b[A-Za-z_][A-Za-z0-9_.]*\.)?\b(?:rt|runtime)\s*(?:==|!=)\s*"(?:podman|container|macos-user)"` +
		`|\b(?:NativeRuntimes|SupportedRuntimes|AllRuntimes)\b`)

// parityMarker matches the declaration. Cheap on purpose: one trailing comment, one word, a
// reason. `// parity: HonoredBy — the AC wsState bind already covers the per-workspace tier`
//
// TRAILING ONLY, and that is the whole reason it is not "a comment anywhere near the site":
// a marker on the line above can be INHERITED by a branch inserted underneath it, which is
// how a new undeclared site would slip through the thing built to stop it. One line, one
// marker, no inheritance.
var parityMarker = regexp.MustCompile(`//\s*parity:\s*(\w+)\s*[-—:]*\s*(.*)$`)

// parityBacklog is the NOT-YET-CLASSIFIED set: how many gate lines in each file carry no
// marker. It is the second half of the distinction this test exists to draw — a ruled
// carve-out is fine, an unclassified branch is the bug — and it is a RATCHET, not a
// permission slip:
//
//   - add a gate line to a file and its count is wrong, so the test fails naming the line;
//   - classify one (write the marker) and the count is wrong the other way, so the test
//     fails telling you to lower the number;
//   - delete a file's last gate line and its row is stale, so the test fails.
//
// The counts are DERIVED, not judged: nothing in these rows says the sites they cover are
// correct. They say nobody has looked. Every row is work, and the file is the unit because a
// per-line key would rot on the next gofmt.
//
// backendcaps.go was the first file classified, as the worked example of what a declaration
// looks like; the markers elsewhere in the package were written since. The rest could not be
// touched in the change that added this test — several of those files were held by another
// agent — so they are recorded rather than silently blessed.
var parityBacklog = map[string]int{
	"assemble.go":            10,
	"assemble_parts.go":      6,
	"backendlimits.go":       1,
	"cgroupresolve_linux.go": 1,
	"helpers.go":             1,
	"hostfiles.go":           1,
	"hostloopback.go":        1,
	"hostprobes.go":          6,
	"inheritscope.go":        2,
	"lifecycle.go":           7,
	"loopholesruntime.go":    3,
	"packfiles.go":           1,
	"packhostgrants.go":      1,
	"perfevents.go":          1,
	"prepare.go":             1,
	"preflight.go":           6,
	"run.go":                 8,
	"storepackages.go":       1,
}

type paritySite struct {
	file string
	line int
	text string
}

func TestEveryBackendBranchIsClassified(t *testing.T) {
	sites := parityGateSites(t)

	// Self-check, the same one the preflight-generator test carries: if the shape this
	// scanner matches ever changes, it stops reading the authority it claims to read and
	// then passes by finding nothing. 50 is a FLOOR, not a census: it sits well under what
	// the pipeline holds, so classifying sites never trips it and only a scanner that has
	// gone blind does.
	if len(sites) < 50 {
		t.Fatalf("found only %d runtime-gated lines in %s — the run pipeline holds far more than "+
			"that (parityBacklog alone records %d still unclassified), so parityGate has stopped "+
			"matching the code and this test is now vacuous",
			len(sites), parityScope, parityBacklogTotal())
	}

	undeclared := map[string][]paritySite{}
	for _, s := range sites {
		m := parityMarker.FindStringSubmatch(s.text)
		if m == nil {
			undeclared[s.file] = append(undeclared[s.file], s)
			continue
		}
		disp, reason := m[1], strings.TrimSpace(m[2])
		if _, ok := parityDispositions[disp]; !ok {
			t.Errorf("%s:%d: `parity: %s` is not one of the dispositions %v.\n  %s",
				s.file, s.line, disp, sortedDispositions(), strings.TrimSpace(s.text))
		}
		if len(reason) < 12 {
			t.Errorf("%s:%d: `parity: %s` carries no reason. A disposition with no reason is a "+
				"label; the reason is what lets the next reader ask whether it still holds — "+
				"which is the question nobody re-asked before #39.\n  %s",
				s.file, s.line, disp, strings.TrimSpace(s.text))
		}
	}

	// A file whose unclassified count does not match its backlog row. Both directions are
	// errors and the messages differ, because the fixes differ.
	for file, sites := range undeclared {
		got := len(sites)
		want, recorded := parityBacklog[file]
		switch {
		case !recorded:
			t.Errorf("%s: %d runtime-gated line(s) here are UNCLASSIFIED and this file is not in "+
				"parityBacklog:\n%s\n"+
				"Every branch on the runtime's identity is a parity decision somebody made. Say "+
				"which one, as a trailing comment on the line:\n\n"+
				"    if rt == \"container\" { // parity: Warned — AC takes no --net selector; "+
				"assemble prints the skip\n\n"+
				"Dispositions: %v (docs/design/backend-parity.md §3).\n"+
				"If you are not ready to decide, add %q: %d to parityBacklog in this file — that "+
				"is the honest state, and it is what the ratchet is for.",
				file, got, siteLines(undeclared[file]), sortedDispositions(), file, got)
		case got > want:
			t.Errorf("%s: %d unclassified runtime-gated line(s), and parityBacklog records %d. "+
				"A NEW backend branch has landed unclassified:\n%s\n"+
				"Mark it with a trailing `// parity: <Disposition> — <reason>`, or raise the "+
				"backlog row and say why in review. Dispositions: %v.",
				file, got, want, siteLines(undeclared[file]), sortedDispositions())
		case got < want:
			t.Errorf("%s: %d unclassified runtime-gated line(s), and parityBacklog still records "+
				"%d. Lower it to %d — a backlog that over-counts hides the next arrival inside "+
				"its own slack.", file, got, want, got)
		}
	}
	for file, want := range parityBacklog {
		if _, ok := undeclared[file]; !ok {
			t.Errorf("parityBacklog records %d unclassified line(s) in %s and there are none "+
				"(the file may have no runtime gates left at all). Delete the row.", want, file)
		}
	}

	classified := len(sites)
	for _, v := range undeclared {
		classified -= len(v)
	}
	t.Logf("backend-parity census over %s: %d gate lines, %d classified, %d in the backlog",
		parityScope, len(sites), classified, len(sites)-classified)
}

// TestTheParityCensusRejectsAnUndeclaredBranch is the guard on the guard, and it is here
// because of AGENTS.md's rule that a test which passes when its subject is deleted is not a
// test. The subject here is not a call site — it is a regex pair — so the mutation is
// performed directly: a line of the exact shape a developer would write is fed through the
// same matchers the census uses, and must come out undeclared.
//
// Without this, weakening parityGate (or widening parityMarker to match any comment) would
// leave TestEveryBackendBranchIsClassified green forever, because a scanner that matches
// nothing reports nothing.
func TestTheParityCensusRejectsAnUndeclaredBranch(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		isGate     bool
		isDeclared bool
	}{
		{"the undeclared branch", `	if rt == "container" {`, true, false},
		{"the negated form", `	if rt != "container" {`, true, false},
		{"a field", `		if in.rt == "container" {`, true, false},
		{"a switch case", `	case rt == "container":`, true, false},
		{"the native list", `	if inStrSlice(paths.NativeRuntimes, rt) {`, true, false},
		{"macos-user by name", `	if rt == "macos-user" {`, true, false},
		{"declared", `	if rt == "container" { // parity: Warned — the launch prints the skip`, true, true},
		{"not a backend gate", `	if o.IsMacOS {`, false, false},
		{"a string that merely mentions one", `	out.print("use podman")`, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parityGate.MatchString(c.line); got != c.isGate {
				t.Fatalf("parityGate matched=%v, want %v, for:\n  %s\n\n"+
					"The census enumerates from this regex. A gate it stops matching is a "+
					"branch nobody has to classify.", got, c.isGate, c.line)
			}
			if !c.isGate {
				return
			}
			if got := parityMarker.MatchString(c.line); got != c.isDeclared {
				t.Fatalf("parityMarker matched=%v, want %v, for:\n  %s", got, c.isDeclared, c.line)
			}
		})
	}

	// The inheritance hole the trailing-only rule exists to close: a marker on the line
	// ABOVE must not declare the line below it.
	above := `	// parity: Honored — AC takes the same flag`
	below := `	if rt == "container" {`
	if !parityMarker.MatchString(above) || parityMarker.MatchString(below) {
		t.Error("a marker on its own line must not reach the next line — if it does, a branch " +
			"inserted under an existing marker inherits its declaration, which is exactly the " +
			"silent arrival this test exists to stop")
	}
}

// parityGateSites reads every non-test .go file in parityScope and returns one site per
// gate LINE. Comment lines are skipped, so the marker convention cannot enumerate itself and
// a gate quoted in a doc comment is not a site.
func parityGateSites(t *testing.T) []paritySite {
	t.Helper()
	entries, err := os.ReadDir(parityScope)
	if err != nil {
		t.Fatalf("cannot read %s, which this test derives its answer from: %v", parityScope, err)
	}
	var out []paritySite
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(parityScope, name))
		if err != nil {
			t.Fatalf("cannot read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if parityGate.MatchString(line) {
				out = append(out, paritySite{file: name, line: i + 1, text: line})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out
}

func siteLines(sites []paritySite) string {
	var b strings.Builder
	for _, s := range sites {
		fmt.Fprintf(&b, "    %s:%d: %s\n", s.file, s.line, strings.TrimSpace(s.text))
	}
	return b.String()
}

// parityBacklogTotal is the backlog's own sum, so the self-check above can quote a number
// that moves with the rows instead of one written down beside them.
func parityBacklogTotal() int {
	total := 0
	for _, n := range parityBacklog {
		total += n
	}
	return total
}

func sortedDispositions() []string {
	out := make([]string, 0, len(parityDispositions))
	for k := range parityDispositions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEveryBackendDeclaresALoopbackDisposition is the census applied to ONE field, and the
// one field is chosen because getting it wrong disarms a FATAL check silently.
//
// YOLO_HOST_LOOPBACK carries the launcher's decision into the jail, and the in-jail witness
// refuses a launch whose jail-facing service is unreachable — but only on the ESCALATING
// values (`requested`, `shared`). Every other value, and an absent variable, never escalates
// (OQ-R3: a host yolo could not ask is never refused for what it cannot help). So a backend
// that quietly comes out `unknown` has the refusal switched off, and nothing says so.
//
// WHY A TABLE RATHER THAN AN ASSERTION PER BACKEND. assemble.go's comment at the decision
// enumerates the shapes that never reach hostloopback.go — podman-in-podman and
// `network.mode: host` — and does NOT name Apple Container, which is a third. The value it
// produces there is correct today and is documented at paths.HostLoopbackEnvVar, one file
// away from where a reader of the branch would look. Writing each backend's answer down
// HERE, beside the value, means a backend whose answer changes has to change this table, and
// a backend added to paths.SupportedRuntimes later has no row and fails.
//
// ⚠ ITS SCOPE IS THE CONTAINER BACKENDS, and that is a real hole, not a formality. The
// variable is emitted while ASSEMBLING A CONTAINER ARGV (jailLoopbackEnvArgs, called from
// assemble.go), and run.go's macos-user arm returns before an argv is ever built — so that
// backend emits no disposition at all and there is nothing here to pin. This test iterates
// paths.SupportedRuntimes rather than paths.AllRuntimes for exactly that reason: a
// macos-user row could only be satisfied by a fixture pretending that backend builds a
// container argv, which would assert a code path no launch takes. What follows is the honest
// statement of the gap: a NATIVE backend that grows a jail-facing service, and therefore a
// disposition to carry, is not covered by anything here, and adding it needs a fixture over
// that backend's own plan (internal/macosuser) rather than a row in this table.
//
// ⚠ IT PINS THE SPELLING, NOT THE JUDGEMENT. That `unknown` is the right answer for Apple
// Container is an open question — OQ-BP-4 in docs/design/backend-parity.md asks whether the
// loophole skip on that backend is still justified, and if the answer is "run them", this
// row becomes wrong and this test is what says so.
func TestEveryBackendDeclaresALoopbackDisposition(t *testing.T) {
	// Every container backend, and what it tells the jail. One that is in
	// paths.SupportedRuntimes and absent from this table fails.
	//
	// The value is a SET, and a set with more than one member must say why it is not one
	// value — because a single-member set is the useful kind and a wide one has to earn its
	// width. podman's is wide for a reason that is a property of the MACHINE rather than of
	// yolo: its disposition is read off `podman info` and off whether the launcher is itself
	// in a container, so it legitimately differs between this repo's own jail (shared, via
	// podman-in-podman) and a CI runner (unknown, the probe stubbed). Apple Container's is a
	// single value because nothing about the host can move it.
	want := map[string]struct {
		allowed []string
		why     string
	}{
		"podman": {
			allowed: []string{paths.HostLoopbackUnknown, paths.HostLoopbackShared,
				paths.HostLoopbackRequested, paths.HostLoopbackUnsupported},
			why: "host-dependent by design: `podman info` and podman-in-podman both move it, " +
				"so the per-host answers are pinned where the host is controlled " +
				"(hostloopback_test.go, hostloopbacksafety_test.go). What is pinned HERE is " +
				"only that this backend says SOMETHING from the closed vocabulary",
		},
		"container": {
			allowed: []string{paths.HostLoopbackUnknown},
			why: "Apple Container is excluded before the network mode is even read — it does " +
				"its own per-container networking and takes no selector — so yolo never asks " +
				"this host for loopback forwarding and must not escalate an unreachable " +
				"service (OQ-R3). ⚠ That also means the FATAL in-jail witness never fires " +
				"here, which is only right while this backend starts no loopholes (OQ-BP-4)",
		},
	}

	for _, rt := range paths.SupportedRuntimes {
		t.Run(rt, func(t *testing.T) {
			env, _, _ := jailEnvForRuntime(t, rt)
			got, ok := env[paths.HostLoopbackEnvVar]
			if !ok {
				t.Fatalf("%s emits no %s at all. Since OQ-R6 the launcher emits one of the "+
					"four values on EVERY launch, so that absent may mean only \"a launcher "+
					"older than the variable\" — a backend that omits it hands the in-jail "+
					"witness the one input it is required to read as \"do not escalate\", and "+
					"the refusal is off with nothing in the output to say so.",
					rt, paths.HostLoopbackEnvVar)
			}
			w, declared := want[rt]
			if !declared {
				t.Fatalf("%s emits %s=%q and no row here declares what that backend is "+
					"supposed to say. Add one with the reason — a disposition nobody chose "+
					"is how a fatal check ends up disabled by accident.",
					rt, paths.HostLoopbackEnvVar, got)
			}
			if !inStrSlice(w.allowed, got) {
				t.Errorf("%s emits %s=%q, which is not among the declared answers %v.\n\n"+
					"Why those are the answers: %s\n\nIf the change is deliberate, move the "+
					"row — and check whether it moves this backend across the "+
					"escalate/never-escalate line, which is what decides whether an "+
					"unreachable jail-facing service REFUSES the launch here.",
					rt, paths.HostLoopbackEnvVar, got, w.allowed, w.why)
			}
			if len(w.allowed) > 1 && len(w.why) < 40 {
				t.Errorf("%s declares %d allowed dispositions and no real reason for the "+
					"width. A wide set asserts almost nothing; say what about the host moves "+
					"it, or narrow it.", rt, len(w.allowed))
			}
		})
	}
}
