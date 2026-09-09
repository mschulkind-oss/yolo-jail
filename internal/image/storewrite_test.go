package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers the ONE decision storewrite.go makes — from which user
// namespace the copier writes podman's store — and it has to be a unit gate over
// an injected answer because the machine that runs it cannot host the failure.
// This jail's podman is rootful (`podman info` → Rootless=false) and its kernel
// has no `apparmor_restrict_unprivileged_userns` knob at all, so the refusal that
// took every container job red on 2026-09-09 is unreproducible here by
// construction. What was measured on a real Ubuntu 24.04 VM is written down in
// storewrite.go's header; what is TESTED here is that yolo picks the right mode
// from a given answer, and that the pipeline actually passes the mode it picked
// to the copy.

// TestStoreWritePrefixIsDecidedByPodmansMode is the whole decision as a table.
//
// The third row is the one with an argument behind it: an unknown answer emits
// NOTHING rather than wrapping, because neither branch is universally safe (a
// rootful podman refuses `unshare`, a restricted rootless one refuses the bare
// copy) and emitting nothing is the only choice that cannot newly break a host
// that works today. Flip that row to a wrapped copy and this fails.
func TestStoreWritePrefixIsDecidedByPodmansMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rootless PodmanRootless
		want     string
	}{
		{"a rootless store is written from inside podman's namespace", RootlessYes, "podman unshare --"},
		{"a rootful store needs no namespace", RootlessNo, ""},
		{"an unknown answer changes nothing about how the copy runs", RootlessUnknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(StoreWritePrefix("podman", tc.rootless), " ")
			if got != tc.want {
				t.Errorf("prefix = %q, want %q", got, tc.want)
			}
		})
	}
	// The separator is load-bearing, not decoration: `podman unshare` parses its own
	// flags first, so without `--` the copier's `--insecure-policy` is read as
	// podman's and the launch dies on an unknown flag that names none of this.
	prefix := StoreWritePrefix("podman", RootlessYes)
	if prefix[len(prefix)-1] != "--" {
		t.Errorf("the prefix does not end in the `--` that protects the copier's own flags: %q", prefix)
	}
	// It names the RUNTIME it was given rather than a literal, so a runtime that is
	// not podman cannot be handed podman's subcommand.
	if got := StoreWritePrefix("podman", RootlessYes)[0]; got != "podman" {
		t.Errorf("prefix[0] = %q, want the runtime it was handed", got)
	}
}

// TestRootlessnessIsReadFromPodmanInfoAndAMissingFieldIsNotFalse pins the probe,
// and the last two rows are the point. `host.security.rootless` decoded into a
// bool makes "the field is absent" and "the store is rootful" the same answer —
// and those want opposite branches, because the absent case is yolo not knowing
// and the rootful case is a positive fact. A struct-of-bools implementation
// passes rows 1-2 and fails rows 3-5.
func TestRootlessnessIsReadFromPodmanInfoAndAMissingFieldIsNotFalse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stdout string
		ok     bool
		want   PodmanRootless
	}{
		{"podman says rootless", `{"host":{"security":{"rootless":true}}}`, true, RootlessYes},
		{"podman says rootful", `{"host":{"security":{"rootless":false}}}`, true, RootlessNo},
		{"the field is absent", `{"host":{"security":{}}}`, true, RootlessUnknown},
		{"podman would not run", ``, false, RootlessUnknown},
		{"the output is not json", `Error: unable to connect`, true, RootlessUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked [][]string
			got := PodmanRootlessness("podman", func(argv []string) (string, bool) {
				asked = append(asked, argv)
				return tc.stdout, tc.ok
			})
			if got != tc.want {
				t.Errorf("rootlessness = %v, want %v", got, tc.want)
			}
			if len(asked) != 1 || strings.Join(asked[0], " ") != "podman info --format json" {
				t.Errorf("probe argv = %v, want one `podman info --format json`", asked)
			}
		})
	}
	// A nil capture is "nothing was asked", which is not an answer either.
	if got := PodmanRootlessness("podman", nil); got != RootlessUnknown {
		t.Errorf("with no way to ask, rootlessness = %v, want unknown", got)
	}
}

// TestARootlessStoreIsWrittenFromInsidePodmansNamespace PINS THE CALL SITE, which
// is the property the pure table above cannot have. Delete AutoLoadImage's
// StoreWritePrefix call — or compute the prefix, print the note, and forget to
// pass it — and the decision table stays green while every rootless launch goes
// back to being refused. This is the test that fails.
func TestARootlessStoreIsWrittenFromInsidePodmansNamespace(t *testing.T) {
	t.Run("rootless: the copy runs inside podman unshare", func(t *testing.T) {
		withBuildDir(t)
		storePath := storeManifest(t, "rootless-image")
		f := newFakeRuntime()
		var out bytes.Buffer
		opts := c2Opts("podman", storePath, f, &out)
		opts.Rootless = func() PodmanRootless { return RootlessYes }

		if res := AutoLoadImage(opts); !res.OK {
			t.Fatalf("the launch failed: %s", out.String())
		}
		if got := strings.Join(f.copiedPrefixes, "|"); got != "podman unshare --" {
			t.Errorf("copy prefix = %q, want %q — the namespace decision is not reaching the copy",
				got, "podman unshare --")
		}
		// §3.4a: a decision with more than one outcome has to say which it took.
		if !strings.Contains(out.String(), "podman unshare") {
			t.Errorf("the launch never said it took the namespace route: %q", out.String())
		}
	})

	t.Run("rootful: the copy runs as itself", func(t *testing.T) {
		withBuildDir(t)
		storePath := storeManifest(t, "rootful-image")
		f := newFakeRuntime()
		var out bytes.Buffer
		opts := c2Opts("podman", storePath, f, &out)
		opts.Rootless = func() PodmanRootless { return RootlessNo }

		if res := AutoLoadImage(opts); !res.OK {
			t.Fatalf("the launch failed: %s", out.String())
		}
		if got := strings.Join(f.copiedPrefixes, "|"); got != "" {
			t.Errorf("copy prefix = %q, want none — `podman unshare` REFUSES on a rootful "+
				"podman, so wrapping here breaks the case that never had the problem", got)
		}
	})

	t.Run("unknown: nothing is added to the argv", func(t *testing.T) {
		withBuildDir(t)
		storePath := storeManifest(t, "unknown-image")
		f := newFakeRuntime()
		var out bytes.Buffer
		opts := c2Opts("podman", storePath, f, &out)
		opts.Rootless = func() PodmanRootless { return RootlessUnknown }

		if res := AutoLoadImage(opts); !res.OK {
			t.Fatalf("the launch failed: %s", out.String())
		}
		if got := strings.Join(f.copiedPrefixes, "|"); got != "" {
			t.Errorf("copy prefix = %q, want none on an unproven fact", got)
		}
	})
}

// TestAnArchiveIsNeverWrapped: the two backends that take an archive write an
// ordinary FILE, whose recorded ownership is data rather than something the
// filesystem has to represent — so there is no subuid mapping to arrange and
// nothing to unshare for. It matters because `podman unshare` is meaningless on
// Apple Container and refused by a podman that is not rootless, so a prefix
// leaking onto this path would break both backends to fix neither.
func TestAnArchiveIsNeverWrapped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime string
		macOS   bool
	}{
		{"apple container", "container", false},
		{"podman on macOS", "podman", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withBuildDir(t)
			storePath := storeManifest(t, "archive-image")
			f := newFakeRuntime()
			var out bytes.Buffer
			opts := acOpts(storePath, f, &out)
			opts.Runtime = tc.runtime
			f.runtime = tc.runtime
			opts.IsMacOS = tc.macOS
			// A rootless answer that must NOT be consulted on this path.
			opts.Rootless = func() PodmanRootless { return RootlessYes }

			if res := AutoLoadImage(opts); !res.OK {
				t.Fatalf("the launch failed: %s", out.String())
			}
			if got := strings.Join(f.copiedPrefixes, "|"); got != "" {
				t.Errorf("archive copy prefix = %q, want none", got)
			}
		})
	}
}

// TestTheCopierArgvCarriesTheNamespacePrefixFirst pins the shape of the composed
// argv. The prefix must LEAD (it is the program that will exec the copier) and the
// copier's own flags must stay behind the copier's own name.
func TestTheCopierArgvCarriesTheNamespacePrefixFirst(t *testing.T) {
	argv := copyArgv(StoreWritePrefix("podman", RootlessYes),
		"/nix/store/x/bin/skopeo", "/nix/store/img.json", "containers-storage:ref:tag")
	want := "podman unshare -- /nix/store/x/bin/skopeo --insecure-policy copy " +
		"nix:/nix/store/img.json containers-storage:ref:tag"
	if got := strings.Join(argv, " "); got != want {
		t.Errorf("argv =\n  %q\nwant\n  %q", got, want)
	}
	// Without a prefix it is exactly the argv this package has always run.
	bare := copyArgv(nil, "/nix/store/x/bin/skopeo", "/nix/store/img.json", "containers-storage:r")
	if got, w := strings.Join(bare, " "), "/nix/store/x/bin/skopeo --insecure-policy copy "+
		"nix:/nix/store/img.json containers-storage:r"; got != w {
		t.Errorf("unprefixed argv =\n  %q\nwant\n  %q", got, w)
	}
}

// TestACopyThroughAPrefixKeepsTheChildsOwnWords drives the REAL copyImage through
// a two-token prefix and answers the two questions a wrapper raises, neither of
// which the argv table can settle:
//
//   - does the copier receive its arguments intact through the wrapper (the `--`
//     is consumed by the wrapper, everything after it is the child's), and
//   - does the CHILD's stderr still reach the user, since a failed copy has to
//     fail loudly with skopeo's own diagnosis rather than the wrapper's exit code.
//
// The stand-in wrapper is `shift 2; exec "$@"`, which is what `podman unshare --`
// does to an argv modulo the namespace it sets up first.
func TestACopyThroughAPrefixKeepsTheChildsOwnWords(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	wrapper := writeScript(t, filepath.Join(dir, "wrapper"), `shift 2; exec "$@"`)
	copier := writeScript(t, filepath.Join(dir, "skopeo"),
		"printf '%s\\n' \"$@\" > "+argsFile+"\n"+
			"echo 'time=... level=fatal msg=\"copying layers: broken pipe\"' >&2\nexit 1")

	var out bytes.Buffer
	prefix := []string{wrapper, "unshare", "--"}
	ok, tail := copyImage(copyArgv(prefix, copier, "/nix/store/abc.json",
		"containers-storage:localhost/yolo-jail:beef"), &out)
	if ok {
		t.Fatal("a copier that exited 1 through the wrapper was reported as success")
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the copier was never reached through the wrapper: %v", err)
	}
	want := "--insecure-policy\ncopy\nnix:/nix/store/abc.json\n" +
		"containers-storage:localhost/yolo-jail:beef\n"
	if string(got) != want {
		t.Errorf("the copier's argv did not survive the wrapper:\n%q\nwant\n%q", string(got), want)
	}
	if !strings.Contains(out.String(), "broken pipe") {
		t.Errorf("the child's own diagnosis did not reach the report: %q", out.String())
	}
	if !strings.Contains(strings.Join(tail, "\n"), "broken pipe") {
		t.Errorf("the child's stderr did not reach the retry decision: %v", tail)
	}
}

// TestARefusedNamespaceIsNotRetried is the retry narrowing, and both polarities
// are needed to pin it: the two MEASURED permanent refusals must run the copier
// exactly once and say why, while everything else keeps the single retry §3.6
// bounds. Delete the denylist and the first two rows run twice; turn the denylist
// into an allowlist of transient causes and the third row stops retrying.
func TestARefusedNamespaceIsNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stderr   string
		wantRuns int64
		wantSays string
	}{
		{
			name:     "the copier could not create the namespace",
			stderr:   "Error during unshare(...): Operation not permitted",
			wantRuns: 1,
			wantSays: "Not retrying",
		},
		{
			name:     "podman refused unshare because it is not rootless",
			stderr:   "Error: please use unshare with rootless",
			wantRuns: 1,
			wantSays: "Not retrying",
		},
		{
			name:     "anything else keeps the one retry",
			stderr:   "time=... level=fatal msg=\"initializing destination: connection reset\"",
			wantRuns: 2,
			wantSays: "Retrying the image copy once",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			counter := filepath.Join(dir, "n")
			copier := writeScript(t, filepath.Join(dir, "skopeo"),
				"printf x >> "+counter+"; echo '"+tc.stderr+"' >&2; exit 1")
			var out bytes.Buffer
			if copyImageWithRetry(copyArgv(nil, copier, "/nix/store/a.json",
				"containers-storage:x:y"), &out) {
				t.Fatal("a copier that always fails was reported as success")
			}
			if n := fileSize(t, counter); n != tc.wantRuns {
				t.Errorf("copier ran %d time(s), want %d", n, tc.wantRuns)
			}
			if !strings.Contains(out.String(), tc.wantSays) {
				t.Errorf("the report does not say %q:\n%s", tc.wantSays, out.String())
			}
		})
	}
}

// TestARefusedNamespaceSaysWhatToLookAt: "no remedy to name" is what §3.6 accepted
// for a failed copy, and for THIS cause it was avoidable. The report has to name
// the two commands that distinguish the two ways a host gets here, or the reader
// is left with a syscall name.
func TestARefusedNamespaceSaysWhatToLookAt(t *testing.T) {
	retry, why := retryWouldHelp([]string{"Error during unshare(...): Operation not permitted"})
	if retry {
		t.Fatal("a refused namespace was classified as worth retrying")
	}
	for _, want := range []string{"/etc/subuid", "podman info", "podman unshare"} {
		if !strings.Contains(why, want) {
			t.Errorf("the reason does not mention %q: %q", want, why)
		}
	}
	if _, why := retryWouldHelp(nil); why != "" {
		t.Errorf("an empty stderr produced a refusal reason: %q", why)
	}
}

// TestUnsharePreflightWarnsBeforeALaunchPaysForIt is `yolo check`'s half. Row 2 is
// the reason it exists: without it the first sign of a host that cannot deliver an
// image is a launch dying partway through a multi-gigabyte copy. Row 5 is the
// tri-state rule — an unproven fact produces no line, because a podman that would
// not answer is already the runtime section's subject and two warnings about one
// fact teach the reader to skip both.
func TestUnsharePreflightWarnsBeforeALaunchPaysForIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rootless PodmanRootless
		hasSh    bool
		probeOK  bool
		wantWarn bool
		wantRan  bool
		wantHint bool
		says     string
	}{
		{"rootless and the namespace works", RootlessYes, true, true, false, true, false, "verified"},
		{"rootless and the namespace is refused", RootlessYes, true, false, true, true, true, "will fail"},
		{"rootless with no /bin/sh to probe with", RootlessYes, false, false, false, false, false, "podman unshare"},
		{"rootful needs no namespace", RootlessNo, true, false, false, false, false, "rootful"},
		{"unknown says nothing at all", RootlessUnknown, true, false, false, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked [][]string
			got := UnsharePreflight("podman", tc.rootless, tc.hasSh,
				func(argv []string) bool {
					asked = append(asked, argv)
					return tc.probeOK
				})
			if got.Warn != tc.wantWarn {
				t.Errorf("warn = %v, want %v (line: %q)", got.Warn, tc.wantWarn, got.Line)
			}
			if (len(asked) > 0) != tc.wantRan {
				t.Errorf("probe ran = %v, want %v", len(asked) > 0, tc.wantRan)
			}
			if (got.Ran != "") != tc.wantRan {
				t.Errorf("reported command = %q, want ran=%v", got.Ran, tc.wantRan)
			}
			if (got.Hint != "") != tc.wantHint {
				t.Errorf("hint = %q, want present=%v", got.Hint, tc.wantHint)
			}
			if tc.says == "" {
				if got.Line != "" {
					t.Errorf("line = %q, want silence on an unproven fact", got.Line)
				}
			} else if !strings.Contains(got.Line, tc.says) {
				t.Errorf("line = %q, want it to mention %q", got.Line, tc.says)
			}
			if tc.wantRan && strings.Join(asked[0], " ") != "podman unshare -- /bin/sh -c :" {
				t.Errorf("probe argv = %v, want the namespace the copy will use", asked[0])
			}
		})
	}
}
