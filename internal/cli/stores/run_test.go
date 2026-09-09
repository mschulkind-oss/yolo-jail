package stores

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNoRecordWritesNothingAtAll is `--no-record`'s whole contract, and it is
// stronger than "appends no line": the ledger DIRECTORY must not appear either.
// A read-only invocation that creates a directory in the user's state dir has
// already broken the promise the flag exists to make.
//
// MUTATION: drop the `if !o.NoRecord` gate in Run and this fails immediately.
func TestNoRecordWritesNothingAtAll(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "npm", "f"), 100)
	o.NoRecord = true

	if rc := Run(o); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if _, err := os.Stat(o.SamplesDir()); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(o.SamplesDir())
		t.Fatalf("--no-record created the ledger dir %s (%d entries); a pure-read run must leave "+
			"no trace", o.SamplesDir(), len(entries))
	}
}

// TestRunRecordsOnlyWhenRecordingIsOn is the pair of the test above, and the one
// that keeps the OQ-BF9 exception honest in BOTH directions: recording is the
// DEFAULT (a command that never records can never report a rate, which is the
// failure the ruling names), and --no-record is the only way to turn it off.
//
// MUTATION 1: delete the record() call in Run — the default case below fails,
// because no sample file exists.
// MUTATION 2: invert or delete the NoRecord gate — the case above fails.
func TestRunRecordsOnlyWhenRecordingIsOn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		noRecord   bool
		wantLedger bool
	}{
		{"default records", false, true},
		{"--no-record does not", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, state := testOptions(t)
			writeFile(t, filepath.Join(state, "cache", "npm", "f"), 100)
			o.NoRecord = tc.noRecord

			Run(o)

			samples := ReadSamples(o.SamplesDir(), "cache.npm")
			if tc.wantLedger && len(samples) != 1 {
				t.Fatalf("recorded %d samples for cache.npm, want exactly 1 — one dated line per "+
					"store per run", len(samples))
			}
			if !tc.wantLedger && len(samples) != 0 {
				t.Fatalf("--no-record still wrote %d sample(s)", len(samples))
			}
			if !tc.wantLedger {
				return
			}
			s := samples[0]
			if s.Bytes != 100 || s.Sizing != SizingMeasured || !s.At.Equal(o.Now().UTC()) {
				t.Errorf("sample = %+v, want 100 bytes / measured / %s", s, o.Now().UTC())
			}
		})
	}
}

// TestOneRunWritesOneLinePerStore pins the cadence half of the ruling ("one
// dated line per store per run"): a second run appends exactly one more line to
// each store's file, never one per section, per subdir walked, or per render.
func TestOneRunWritesOneLinePerStore(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "npm", "f"), 100)
	writeFile(t, filepath.Join(state, "mise", "f"), 5)

	Run(o)
	first := len(ReadSamples(o.SamplesDir(), "cache.npm"))
	o.Now = func() time.Time { return time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC) }
	Run(o)
	second := len(ReadSamples(o.SamplesDir(), "cache.npm"))

	if first != 1 || second != 2 {
		t.Errorf("sample counts after one and two runs = %d, %d; want 1, 2", first, second)
	}
	if got := len(ReadSamples(o.SamplesDir(), "state.mise")); got != 2 {
		t.Errorf("state.mise has %d samples after two runs, want 2 — every store gets its own line", got)
	}
}

// TestRunReportsGrowthFromTheLedgerBeforeRecording pins BOTH halves of the
// growth path through Run: that the ledger is READ at all (delete attachGrowth's
// call and no store has a rate), and that it is read BEFORE this run's own line
// is appended.
//
// The ordering half needs a ledger already AT the bound, because that is where
// the difference shows: append first and the oldest sample — the one that gives
// the long baseline — is evicted by the same write, so the rate silently
// recomputes against a two-hour-old sample and reports 120 B/d instead of 100.
func TestRunReportsGrowthFromTheLedgerBeforeRecording(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "npm", "f"), 2000)

	// Oldest: ten days back, half the size. Then fill the ledger to its bound with
	// recent samples, so a write BEFORE the read would push the oldest one out.
	if err := AppendSample(o.SamplesDir(), "cache.npm",
		Sample{At: o.Now().Add(-10 * 24 * time.Hour), Sizing: SizingMeasured, Bytes: 1000}); err != nil {
		t.Fatal(err)
	}
	for i := range MaxSamples - 1 {
		at := o.Now().Add(-2*time.Hour + time.Duration(i)*time.Minute)
		if err := AppendSample(o.SamplesDir(), "cache.npm",
			Sample{At: at, Sizing: SizingMeasured, Bytes: 1990}); err != nil {
			t.Fatal(err)
		}
	}

	out := new(strings.Builder)
	o.Out, o.JSON = out, true
	if rc := Run(o); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatal(err)
	}

	var npm Store
	for _, s := range rep.Stores {
		if s.Key == "cache.npm" {
			npm = s
		}
	}
	if npm.Growth == nil {
		t.Fatal("no growth reported despite a ledger full of dated samples — Run is not reading the ledger")
	}
	if npm.Growth.BytesPerDay != 100 {
		t.Errorf("growth = %d B/d, want 100 (1000 bytes over 10 days). 120 means this run's own "+
			"sample was appended before the ledger was read, evicting the oldest one",
			npm.Growth.BytesPerDay)
	}
}

// TestRunNeverMutatesAStore is the forbidden-behavior half of §5.5, asserted
// rather than assumed: the only thing on disk that may differ after a run is the
// ledger.
func TestRunNeverMutatesAStore(t *testing.T) {
	o, state := testOptions(t)
	files := map[string]int{
		filepath.Join(state, "cache", "npm", "a"):              100,
		filepath.Join(state, "cache", "pants", "b"):            200,
		filepath.Join(state, "captures", "c"):                  300,
		filepath.Join(o.NixStore, "x-yolo-jail-go-0-dev", "d"): 400,
	}
	for p, n := range files {
		writeFile(t, p, n)
	}
	before := snapshot(t, state, o.NixStore)

	Run(o)

	after := snapshot(t, state, o.NixStore)
	for path, size := range before {
		if got, ok := after[path]; !ok || got != size {
			t.Errorf("%s changed or disappeared (%d -> %d, present=%v)", path, size, got, ok)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok && !strings.HasPrefix(path, o.SamplesDir()) {
			t.Errorf("the run created %s, which is not the ledger", path)
		}
	}
}

func snapshot(t *testing.T, roots ...string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if st, err := os.Stat(path); err == nil {
				out[path] = st.Size()
			}
			return nil
		})
	}
	return out
}

// TestJSONIsTheAgentSurface: --json prints the whole inventory as one object,
// with the frame, the sizing of every figure, and the reclaimer — the fields an
// agent would otherwise have to scrape out of the table.
func TestJSONIsTheAgentSurface(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "npm", "f"), 100)
	out := new(strings.Builder)
	o.Out = out
	o.JSON = true

	if rc := Run(o); rc != 0 {
		t.Fatalf("Run --json = %d, want 0", rc)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("--json did not print one JSON object: %v\n%s", err, out.String())
	}
	if rep.Frame == "" || rep.Budget == "" || len(rep.Stores) == 0 {
		t.Fatalf("--json object is missing the frame, the budget or the stores: %+v", rep)
	}
	found := false
	for _, s := range rep.Stores {
		if s.Key == "cache.npm" {
			found = true
			if s.Sizing != SizingMeasured || s.Bytes != 100 {
				t.Errorf("cache.npm = %q / %d bytes, want measured / 100", s.Sizing, s.Bytes)
			}
		}
	}
	if !found {
		t.Error("--json omitted a store the human report shows")
	}
}

// TestAnUnreadableStoreDoesNotFailTheCommand: "One unreadable store never fails
// the command: a partial inventory is the useful answer, and exiting non-zero on
// it would make the command unusable exactly where it matters most."
func TestAnUnreadableStoreDoesNotFailTheCommand(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "npm", "f"), 100)
	o.Walk = func(root string, _ time.Time, _ time.Duration, _ func() time.Time) (WalkResult, error) {
		return WalkResult{}, os.ErrPermission
	}
	out := new(strings.Builder)
	o.Out = out

	if rc := Run(o); rc != 0 {
		t.Fatalf("Run with every store unreadable = %d, want 0", rc)
	}
	if !strings.Contains(out.String(), "unknown") {
		t.Errorf("an unreadable store was not reported as unknown:\n%s", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "cache/npm") && strings.Contains(line, "0 B") {
			t.Errorf("an unreadable store rendered as a byte count; unknown is not zero:\n%s", line)
		}
	}
	// It is recorded AS unknown, so the ledger cannot later be read as a run that
	// never happened.
	samples := ReadSamples(o.SamplesDir(), "cache.npm")
	if len(samples) != 1 || samples[0].Sizing != SizingUnknown {
		t.Errorf("recorded %+v, want one sample marked unknown", samples)
	}
}
