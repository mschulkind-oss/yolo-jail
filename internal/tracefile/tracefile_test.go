package tracefile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Fixtures are literal trace lines per the plan's line contract. Times are nanoseconds since the
// epoch on a base of 1700000000000000000, so the last nine digits read as an offset in ns and
// 1700000001250000000 is 1.25 s in.

const (
	tid   = "4bf92f3577b34da6a3ce929d0e0e4736"
	tidB  = "0af7651916cd43dd8448eb211c80319c"
	tidC  = "b7ad6b7169203331b7ad6b7169203331"
	spanL = "00f067aa0ba902b7" // launch
	spanA = "a000000000000001"
	spanB = "b000000000000002"
	spanC = "c000000000000003"
	spanD = "d000000000000004"
	spanE = "e000000000000005"
)

func writeTrace(t *testing.T, root, id string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, files map[string]string) *Trace {
	t.Helper()
	tr, err := ReadDir(writeTrace(t, t.TempDir(), tid, files))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func span(t *testing.T, tr *Trace, id string) *Span {
	t.Helper()
	for _, s := range tr.Spans {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("span %s not in trace; spans: %v", id, names(tr.Spans))
	return nil
}

func names(ss []*Span) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name+"/"+s.ID)
	}
	return out
}

func parentName(s *Span) string {
	if s.Parent == nil {
		return "<lane root>"
	}
	return s.Parent.Name
}

func lane(t *testing.T, tr *Trace, file string) *Lane {
	t.Helper()
	for _, l := range tr.Lanes {
		if l.File == file {
			return l
		}
	}
	t.Fatalf("lane %s not read; skipped: %v", file, tr.Skipped)
	return nil
}

const launcherHeader = `{"k":"lane","name":"launcher","pid":100,"trace":"` + tid + `"}`

// TestTheContractsOwnExampleLinesRead pins the two example lines the plan's contract shows.
const canonicalMeta = `{"trace":"` + tid + `","kind":"launch","start":123,"jail":"test","workspace":"ab12","version":"test","commit":"test"}`
const canonicalStart = `{"t":1700000000000000000,"k":"start","name":"invocation","span":"` + spanL + `","launcher_root":true}`
const canonicalEnd = `{"t":1700000000000000100,"k":"end","name":"invocation","span":"` + spanL + `"}`

func TestLauncherRootTypesAndAuthority(t *testing.T) {
	for _, marker := range []string{"absent", "true", "false", "null", "\"secret-value\"", "1", "[]", "{}"} {
		t.Run(marker, func(t *testing.T) {
			start := canonicalStart
			if marker == "absent" {
				start = strings.Replace(start, `,"launcher_root":true`, "", 1)
			} else {
				start = strings.Replace(start, `"launcher_root":true`, `"launcher_root":`+marker, 1)
			}
			tr := read(t, map[string]string{MetaFile: canonicalMeta, "launcher-100.jsonl": lines(launcherHeader, start, canonicalEnd)})
			want := "invocation"
			if marker == "true" {
				want = "launch"
			}
			if s := span(t, tr, spanL); s.Name != want || s.Start != 1700000000000000000 || s.End != 1700000000000000100 {
				t.Fatal(s)
			}
			if marker != "true" && marker != "absent" && len(tr.Anomalies) == 0 {
				t.Fatal("no bounded designation diagnostic")
			}
			if strings.Contains(strings.Join(tr.Anomalies, " "), "secret-value") {
				t.Fatal("private marker value leaked")
			}
		})
	}
	for _, condition := range []string{"wrong_lane", "duplicate_start", "cross_lane", "end_marker", "parent"} {
		t.Run(condition, func(t *testing.T) {
			files := map[string]string{MetaFile: canonicalMeta, "launcher-100.jsonl": lines(launcherHeader, canonicalStart, canonicalEnd)}
			switch condition {
			case "wrong_lane":
				files["launcher-100.jsonl"] = strings.Replace(files["launcher-100.jsonl"], `"name":"launcher"`, `"name":"keeper"`, 1)
			case "duplicate_start":
				files["launcher-100.jsonl"] = lines(launcherHeader, canonicalStart, canonicalStart, canonicalEnd)
			case "cross_lane":
				files["keeper-200.jsonl"] = lines(strings.Replace(launcherHeader, `"name":"launcher"`, `"name":"keeper"`, 1), strings.Replace(canonicalStart, `,"launcher_root":true`, "", 1), canonicalEnd)
			case "end_marker":
				files["launcher-100.jsonl"] = lines(launcherHeader, strings.Replace(canonicalStart, `,"launcher_root":true`, "", 1), strings.Replace(canonicalEnd, `}`, `,"launcher_root":true}`, 1))
			case "parent":
				files["launcher-100.jsonl"] = lines(launcherHeader, strings.Replace(canonicalStart, `}`, `,"parent":"`+spanA+`"}`, 1), canonicalEnd)
			}
			tr := read(t, files)
			if span(t, tr, spanL).Name != "invocation" || len(tr.Anomalies) == 0 {
				t.Fatal("conflict resolved", tr.Anomalies)
			}
		})
	}
}

func TestLauncherRootInvalidUnrelatedMarkerPreservesValidAuthority(t *testing.T) {
	tr := read(t, map[string]string{MetaFile: canonicalMeta, "launcher-100.jsonl": lines(launcherHeader, canonicalStart,
		`{"t":1700000000000000010,"k":"start","name":"ordinary","span":"`+spanA+`","launcher_root":false}`,
		`{"t":1700000000000000020,"k":"end","name":"ordinary","span":"`+spanA+`"}`, canonicalEnd)})
	if span(t, tr, spanL).Name != "launch" || span(t, tr, spanA).Name != "ordinary" || len(tr.Anomalies) == 0 {
		t.Fatal("unrelated marker affected valid root or base event")
	}
}

func TestCanonicalLauncherRootUsesPublicMetadataOnly(t *testing.T) {
	for _, meta := range []string{canonicalMeta, strings.Replace(canonicalMeta, `"launch"`, `"attach"`, 1), strings.Replace(canonicalMeta, `"launch"`, `"incomplete"`, 1), strings.Replace(canonicalMeta, `"launch"`, `"host"`, 1), strings.Replace(canonicalMeta, `"start":123`, `"start":0`, 1), strings.Replace(canonicalMeta, `"start":123`, `"start":null`, 1), strings.Replace(canonicalMeta, `}`, `,"extra":true}`, 1), strings.Replace(canonicalMeta, `"commit":"test"`, `"commit":null`, 1), ""} {
		t.Run(fmt.Sprintf("meta_%d", len(meta))+meta, func(t *testing.T) {
			files := map[string]string{"launcher-100.jsonl": lines(launcherHeader, canonicalStart, canonicalEnd)}
			if meta != "" {
				files[MetaFile] = meta
			}
			dir := writeTrace(t, t.TempDir(), tid, files)
			before, _ := os.ReadDir(dir)
			tr, err := ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadDir(dir)
			if len(before) != len(after) {
				t.Fatal("read created controls")
			}
			want := "invocation"
			if meta == canonicalMeta {
				want = "launch"
			}
			if meta == strings.Replace(canonicalMeta, `"launch"`, `"attach"`, 1) {
				want = "attach"
			}
			if len(tr.Spans) != 1 || span(t, tr, spanL).Name != want {
				t.Fatal("canonical authority", names(tr.Spans))
			}
		})
	}
}

func TestAttributesRemainEightPerSpanAcrossRecords(t *testing.T) {
	start := strings.Replace(canonicalStart, `}`, `,"attrs":{"a":"`+strings.Repeat("v", MaxAttrValue+1)+`","b":"2","c":"3","d":"4","e":"5"}}`, 1)
	end := strings.Replace(canonicalEnd, `}`, `,"attrs":{"f":"6","g":"7","h":"8","i":"9","j":"10"}}`, 1)
	tr := read(t, map[string]string{MetaFile: canonicalMeta, "launcher-100.jsonl": lines(launcherHeader, start, end)})
	s := span(t, tr, spanL)
	if len(s.Attrs) != 8 || s.Name != "launch" || len(s.Attrs["a"]) != MaxAttrValue || s.Attrs["f"] != "6" || s.Attrs["h"] != "8" {
		t.Fatal("per span attrs across observed start/end")
	}
}

func TestTheContractsOwnExampleLinesRead(t *testing.T) {
	tr := read(t, map[string]string{"keeper-200.jsonl": lines(
		`{"k":"lane","name":"keeper","pid":200,"trace":"`+tid+`","parent":"03b1c7e2a94d5f60"}`,
		`{"t":1795111105285000000,"k":"start","name":"launch.start_loopholes","span":"9f2c41d07be3a815","parent":"03b1c7e2a94d5f60"}`,
		`{"t":1795111106570000000,"k":"end","name":"launch.start_loopholes","span":"9f2c41d07be3a815","dur":1285000000}`,
	)})
	s := span(t, tr, "9f2c41d07be3a815")
	if !s.Ended || s.Duration() != 1285000000 || s.Name != "launch.start_loopholes" {
		t.Fatalf("span = %+v", s)
	}
	l := lane(t, tr, "keeper-200.jsonl")
	if l.Name != "keeper" || l.PID != 200 || l.ParentID != "03b1c7e2a94d5f60" || l.Dropped != 0 {
		t.Fatalf("lane = %+v", l)
	}
	// The parent it names is in no file of this trace: the span is placed by enclosure, and the
	// reader says why.
	if s.Parent != nil || len(tr.Anomalies) != 2 {
		t.Fatalf("parent %v, anomalies %q", s.Parent, tr.Anomalies)
	}
}

func TestParentPlacement(t *testing.T) {
	tr := read(t, map[string]string{"launcher-100.jsonl": lines(
		launcherHeader,
		`{"t":1700000000000000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
		`{"t":1700000001000000000,"k":"start","name":"outer","span":"`+spanA+`"}`,
		`{"t":1700000002000000000,"k":"start","name":"inner","span":"`+spanB+`"}`,
		`{"t":1700000003000000000,"k":"end","name":"inner","span":"`+spanB+`","dur":1000000000}`,
		// explicit parent wins over the shorter enclosing "outer"
		`{"t":1700000003500000000,"k":"start","name":"explicit","span":"`+spanC+`","parent":"`+spanL+`"}`,
		`{"t":1700000003600000000,"k":"end","name":"explicit","span":"`+spanC+`","dur":100000000}`,
		`{"t":1700000005000000000,"k":"end","name":"outer","span":"`+spanA+`","dur":4000000000}`,
		// a record: an end with no start, placed by its duration inside "outer"
		`{"t":1700000004500000000,"k":"record","name":"recorded","span":"`+spanD+`","dur":500000000}`,
		`{"t":1700000010000000000,"k":"end","name":"launch","span":"`+spanL+`","dur":10000000000}`,
		// open across "launch"'s end: no span of the lane encloses it, so the lane root
		`{"t":1700000009000000000,"k":"start","name":"straddle","span":"`+spanE+`"}`,
		`{"t":1700000011000000000,"k":"end","name":"straddle","span":"`+spanE+`","dur":2000000000}`,
	)})
	for id, want := range map[string]string{
		spanL: "<lane root>", spanA: "launch", spanB: "outer", spanC: "launch",
		spanD: "outer", spanE: "<lane root>",
	} {
		if got := parentName(span(t, tr, id)); got != want {
			t.Errorf("parent of %s = %s, want %s", span(t, tr, id).Name, got, want)
		}
	}
	if !span(t, tr, spanC).Explicit || span(t, tr, spanB).Explicit {
		t.Error("Explicit set wrongly")
	}
	d := span(t, tr, spanD)
	if !d.Recorded || d.Start != 1700000004000000000 {
		t.Errorf("record placed at %d, recorded=%v", d.Start, d.Recorded)
	}
}

// TestEnclosurePicksTheShortestAndTiesGoInnermost: two spans with the same interval nest, the one
// written second inside the first, and never form a cycle.
func TestEnclosurePicksTheShortestAndTiesGoInnermost(t *testing.T) {
	tr := read(t, map[string]string{"launcher-100.jsonl": lines(
		launcherHeader,
		`{"t":1700000000000000000,"k":"start","name":"long","span":"`+spanA+`"}`,
		`{"t":1700000001000000000,"k":"start","name":"twin1","span":"`+spanB+`"}`,
		`{"t":1700000001000000000,"k":"start","name":"twin2","span":"`+spanC+`"}`,
		`{"t":1700000001500000000,"k":"start","name":"leaf","span":"`+spanD+`"}`,
		`{"t":1700000001600000000,"k":"end","span":"`+spanD+`"}`,
		`{"t":1700000002000000000,"k":"end","span":"`+spanB+`"}`,
		`{"t":1700000002000000000,"k":"end","span":"`+spanC+`"}`,
		`{"t":1700000009000000000,"k":"end","span":"`+spanA+`"}`,
	)})
	if p := parentName(span(t, tr, spanC)); p != "twin1" {
		t.Errorf("twin2's parent = %s", p)
	}
	if p := parentName(span(t, tr, spanB)); p != "long" {
		t.Errorf("twin1's parent = %s", p)
	}
	if p := parentName(span(t, tr, spanD)); p != "twin2" {
		t.Errorf("leaf's parent = %s", p)
	}
}

func TestAStartWithNoEndIsReportedAndEndsWithTheTrace(t *testing.T) {
	tr := read(t, map[string]string{"launcher-100.jsonl": lines(
		launcherHeader,
		`{"t":1700000000000000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
		`{"t":1700000001000000000,"k":"start","name":"container.boot.hung","span":"`+spanA+`"}`,
		`{"t":1700000002000000000,"k":"start","name":"later","span":"`+spanB+`"}`,
		`{"t":1700000003000000000,"k":"end","span":"`+spanB+`"}`,
	)})
	hung := span(t, tr, spanA)
	if hung.Ended || hung.End != 1700000003000000000 || parentName(hung) != "launch" {
		t.Fatalf("hung = %+v parent %s", hung, parentName(hung))
	}
	if p := parentName(span(t, tr, spanB)); p != "container.boot.hung" {
		t.Errorf("later's parent = %s", p)
	}
	sum := Summarize(tr, Options{})
	if len(sum.NotEnded) != 2 || sum.NotEnded[0].ID != spanL || sum.NotEnded[1].ID != spanA {
		t.Errorf("NotEnded = %v", names(sum.NotEnded))
	}
	if sum.WindowEnded {
		t.Error("no session.exec, yet the window ended")
	}
}

func TestUnattributedTimeCountsOverlappingChildrenOnce(t *testing.T) {
	tr := read(t, map[string]string{"launcher-100.jsonl": lines(
		launcherHeader,
		`{"t":1700000000000000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
		`{"t":1700000001000000000,"k":"start","name":"one","span":"`+spanA+`","parent":"`+spanL+`"}`,
		`{"t":1700000002000000000,"k":"start","name":"two","span":"`+spanB+`","parent":"`+spanL+`"}`,
		`{"t":1700000003000000000,"k":"end","span":"`+spanA+`"}`,
		`{"t":1700000004000000000,"k":"end","span":"`+spanB+`"}`,
		`{"t":1700000006000000000,"k":"start","name":"three","span":"`+spanC+`"}`,
		`{"t":1700000006050000000,"k":"end","span":"`+spanC+`"}`,
		`{"t":1700000006100000000,"k":"start","name":"four","span":"`+spanD+`"}`,
		`{"t":1700000007000000000,"k":"end","span":"`+spanD+`"}`,
		`{"t":1700000010000000000,"k":"end","span":"`+spanL+`"}`,
	)})
	sum := Summarize(tr, Options{})
	// launch [0,10] less [1,4] (one and two, overlapping) less [6,6.05] less [6.1,7]:
	// [0,1] + [4,6] + [6.05,6.1] + [7,10] = 1 + 2 + 0.05 + 3 = 6.05 s.
	if got := sum.Top[0]; got.Span.Name != "launch" || got.Own != 6050*time.Millisecond {
		t.Fatalf("top = %s %v", got.Span.Name, got.Own)
	}
	if sum.Unattributed != 6050*time.Millisecond {
		t.Errorf("Unattributed = %v", sum.Unattributed)
	}
	type g struct {
		before, after string
		d             time.Duration
	}
	var got []g
	for _, gap := range sum.Gaps {
		if gap.Within == nil || gap.Within.Name != "launch" {
			t.Errorf("gap within %v", gap.Within)
		}
		got = append(got, g{gap.Before, gap.After, gap.Duration()})
	}
	want := []g{{"", "one", time.Second}, {"two", "three", 2 * time.Second}, {"four", "", 3 * time.Second}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("gaps = %v, want %v (the 50 ms one is under the threshold)", got, want)
	}
}

func TestAFileWithAnUnreadableFirstLineIsSkippedAndNamed(t *testing.T) {
	tr := read(t, map[string]string{
		"launcher-100.jsonl": lines(launcherHeader,
			`{"t":1700000000000000000,"k":"start","name":"launch","span":"`+spanL+`","future_field":{"x":[1,2]}}`,
			`{"t":1700000001000000000,"k":"end","span":"`+spanL+`","also_new":true}`),
		"keeper-200.jsonl":  lines(`not json`, `{"t":1700000000500000000,"k":"mark","name":"x"}`),
		"service-300.jsonl": lines(`{"t":1700000000500000000,"k":"mark","name":"no header"}`),
		"other-400.jsonl":   lines(`{"k":"lane","name":"other","pid":400,"trace":"` + tidB + `"}`),
		"empty-500.jsonl":   "",
		"notes.txt":         "ignored",
	})
	if len(tr.Lanes) != 1 || tr.Lanes[0].Dropped != 0 || !span(t, tr, spanL).Ended {
		t.Fatalf("lanes %+v (unknown fields must be ignored, not drop the line)", tr.Lanes)
	}
	reasons := map[string]string{}
	for _, s := range tr.Skipped {
		reasons[filepath.Base(s.Path)] = s.Reason
	}
	for file, want := range map[string]string{
		"keeper-200.jsonl":  "its first line is not a lane line",
		"service-300.jsonl": "its first line is not a lane line",
		"other-400.jsonl":   "its first line names another trace",
		"empty-500.jsonl":   "empty",
	} {
		if reasons[file] != want {
			t.Errorf("%s skipped for %q, want %q", file, reasons[file], want)
		}
	}
	if _, named := reasons["notes.txt"]; named || len(reasons) != 4 {
		t.Errorf("skipped = %v", reasons)
	}
}

// TestMalformedLinesAreDroppedAndCounted covers the untrusted-input cases one line at a time;
// each bad line is dropped and the rest of the file still reads.
func TestMalformedLinesAreDroppedAndCounted(t *testing.T) {
	bad := []string{
		`{"t":1700000000100000000,"k":"start","name":"x","span":"A000000000000001"}`,            // uppercase id
		`{"t":1700000000100000000,"k":"start","name":"x","span":"0000000000000000"}`,            // zero id
		`{"t":1700000000100000000,"k":"start","name":"x","span":"a00000000000001"}`,             // short id
		`{"t":1700000000100000000,"k":"start","name":"x","span":"` + spanA + `","parent":"zz"}`, // bad parent
		`{"t":1700000000100000000,"k":"start","name":"","span":"` + spanA + `"}`,                // no name
		`{"t":1700000000100000000,"k":"start","name":"\u0007\u001b","span":"` + spanA + `"}`,    // name only controls
		`{"t":-5,"k":"mark","name":"x"}`,                                                        // negative time
		`{"t":1700000000100000000,"k":"bogus","name":"x"}`,                                      // unknown kind
		`{"t":1700000000100000000,"k":"record","name":"x","span":"` + spanB + `","dur":-1}`,     // negative dur
		`{"t":1700000000100000000,"k":"record","name":"x","span":"` + spanB + `"}`,              // record, no dur
		`{"t":1700000000100000000,"k":"record","span":"` + spanB + `","dur":5}`,                 // record, no name
		`{"t":1700000000100000000,"k":"mark","name":"x","attrs":{"n":3}}`,                       // non-string attr
		`{"t":"soon","k":"mark","name":"x"}`,                                                    // wrong type
		`{"k":"lane","name":"again","pid":1,"trace":"` + tid + `"}`,                             // lane line mid-file
		`[1,2,3]`, // not an object
		`{"t":1700000000100000000,"k":"mark","name":"x"`,                                                     // cut short
		`{"t":1700000000300000000,"k":"start","name":"dup","span":"` + spanC + `"}`,                          // start reusing an id
		`{"t":1700000000200000000,"k":"end","span":"` + spanD + `"}`,                                         // end before its start
		`{"t":1700000000400000000,"k":"record","name":"dup","span":"` + spanC + `","dur":1}`,                 // record reusing an id
		`{"t":1700000000100000000,"k":"start","name":"x","span":"` + spanA + `","parent":"` + spanA + `"} x`, // trailing junk
	}
	all := []string{launcherHeader,
		`{"t":1700000000000000000,"k":"start","name":"ok\u001b[31m","span":"` + spanC + `"}`,
		`{"t":1700000000300000000,"k":"start","name":"late","span":"` + spanD + `"}`,
	}
	all = append(all, bad...)
	all = append(all, strings.Repeat("x", MaxLineBytes+10)) // over-long line
	all = append(all, `{"t":1700000001000000000,"k":"end","span":"`+spanC+`"}`)
	tr := read(t, map[string]string{"launcher-100.jsonl": lines(all...)})
	l := tr.Lanes[0]
	// the end-before-start line is dropped and "late" stays open; spanD's "end" is one of bad.
	if l.Dropped != len(bad)+1 {
		t.Errorf("Dropped = %d, want %d", l.Dropped, len(bad)+1)
	}
	if len(l.Spans) != 2 || !span(t, tr, spanC).Ended || span(t, tr, spanD).Ended {
		t.Errorf("spans = %v", names(l.Spans))
	}
	if n := span(t, tr, spanC).Name; n != "ok[31m" {
		t.Errorf("name %q kept a control character", n)
	}
	if l.Truncated {
		t.Error("an over-long line truncated the file")
	}
}

func TestATruncatedLastLineIsDroppedAndACompleteOneIsKept(t *testing.T) {
	head := launcherHeader + "\n" +
		`{"t":1700000000000000000,"k":"start","name":"launch","span":"` + spanL + `"}` + "\n"
	cut := read(t, map[string]string{"launcher-100.jsonl": head + `{"t":1700000001000000000,"k":"end","sp`})
	if l := cut.Lanes[0]; l.Dropped != 1 || l.Truncated || span(t, cut, spanL).Ended {
		t.Errorf("cut: %+v", l)
	}
	whole := read(t, map[string]string{"launcher-100.jsonl": head + `{"t":1700000001000000000,"k":"end","span":"` + spanL + `"}`})
	if l := whole.Lanes[0]; l.Dropped != 0 || !span(t, whole, spanL).Ended {
		t.Errorf("whole: %+v", l)
	}
}

func TestAFilePastTheLineBoundIsReadUpToIt(t *testing.T) {
	var b strings.Builder
	b.WriteString(launcherHeader + "\n")
	for i := 0; i < MaxLinesPerFile+5; i++ {
		fmt.Fprintf(&b, `{"t":%d,"k":"mark","name":"m"}`+"\n", 1700000000000000000+i)
	}
	tr := read(t, map[string]string{"launcher-100.jsonl": b.String()})
	l := tr.Lanes[0]
	if !l.Truncated || len(l.Events) != MaxLinesPerFile-1 {
		t.Errorf("Truncated=%v events=%d", l.Truncated, len(l.Events))
	}
}

// TestInterleavedProcesses: two writers of one trace, whose lines interleave in time, and a
// writer whose concurrent same-named spans end out of order. Enclosure stays inside a lane; the
// keeper's lane hangs under the launcher span its first line names.
func TestInterleavedProcesses(t *testing.T) {
	tr := read(t, map[string]string{
		"launcher-100.jsonl": lines(launcherHeader,
			`{"t":1700000000000000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
			`{"t":1700000001000000000,"k":"start","name":"launch.keeper","span":"`+spanA+`"}`,
			`{"t":1700000002000000000,"k":"start","name":"preparation.check","span":"`+spanB+`","attrs":{"key":"fork-a"}}`,
			`{"t":1700000002100000000,"k":"start","name":"preparation.check","span":"`+spanC+`","attrs":{"key":"fork-b"}}`,
			`{"t":1700000002900000000,"k":"end","name":"preparation.check","span":"`+spanC+`"}`,
			`{"t":1700000003000000000,"k":"end","name":"preparation.check","span":"`+spanB+`"}`,
			`{"t":1700000008000000000,"k":"end","span":"`+spanA+`"}`,
			`{"t":1700000009000000000,"k":"mark","name":"jail_main.spawned"}`,
			`{"t":1700000010000000000,"k":"end","span":"`+spanL+`"}`),
		"keeper-200.jsonl": lines(
			`{"k":"lane","name":"keeper","pid":200,"trace":"`+tid+`","parent":"`+spanA+`"}`,
			// starts inside the launcher's preparation.check in time, and must not land there
			`{"t":1700000002500000000,"k":"start","name":"launch.start_loopholes","span":"`+spanD+`"}`,
			`{"t":1700000002600000000,"k":"start","name":"preparation.check","span":"`+spanE+`"}`,
			`{"t":1700000002700000000,"k":"end","span":"`+spanE+`"}`,
			`{"t":1700000004000000000,"k":"end","span":"`+spanD+`"}`,
			`{"t":1700000009000000000,"k":"mark","name":"jail_main.spawned"}`),
	})
	b, c := span(t, tr, spanB), span(t, tr, spanC)
	if b.Duration() != time.Second.Nanoseconds() || c.Duration() != 800*time.Millisecond.Nanoseconds() {
		t.Errorf("same-named concurrent spans mismatched their ends: %d %d", b.Duration(), c.Duration())
	}
	if b.Attrs["key"] != "fork-a" || c.Attrs["key"] != "fork-b" {
		t.Errorf("attrs %v %v", b.Attrs, c.Attrs)
	}
	// Enclosure in time puts the second check under the first, which encloses it: the misplacement
	// the design accepts for flat call sites (§12, TR-D7) until such a caller passes a parent.
	if p := parentName(c); p != "preparation.check" || c.Parent != b {
		t.Errorf("concurrent check placed under %s", p)
	}
	if p := parentName(span(t, tr, spanD)); p != "<lane root>" {
		t.Errorf("keeper span placed under %s, across lanes", p)
	}
	if p := parentName(span(t, tr, spanE)); p != "launch.start_loopholes" {
		t.Errorf("keeper check placed under %s", p)
	}
	keeper := lane(t, tr, "keeper-200.jsonl")
	if keeper.ParentSpan != span(t, tr, spanA) || len(span(t, tr, spanA).ChildLanes) != 1 {
		t.Errorf("keeper lane not under launch.keeper")
	}
	sum := Summarize(tr, Options{})
	// launch.keeper [1,8] holds two checks [2,3] and the keeper lane [2.5,9]: own [1,2] = 1 s.
	for _, o := range sum.Top {
		if o.Span.ID == spanA && o.Own != time.Second {
			t.Errorf("launch.keeper own = %v", o.Own)
		}
	}
	shares := map[string]LaneShare{}
	for _, ls := range sum.Lanes {
		shares[ls.Lane.Name] = ls
	}
	if shares["keeper"].Busy != 1500*time.Millisecond || shares["launcher"].Busy != 10*time.Second {
		t.Errorf("shares = %+v", shares)
	}
}

func TestTheJailLaneAndTheStartupWindow(t *testing.T) {
	tr := read(t, map[string]string{
		"launcher-100.jsonl": lines(launcherHeader,
			`{"t":1700000000500000000,"k":"record","name":"process.wrapper","span":"`+spanA+`","dur":500000000}`,
			`{"t":1700000001000000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
			`{"t":1700000020000000000,"k":"end","span":"`+spanL+`"}`),
		// no lane line; ids minted by the host's reader, parented explicitly
		JailFile: lines(
			`{"t":1699999990000000000,"k":"mark","name":"clock.skewed"}`,
			`{"t":1700000003000000000,"k":"start","name":"container.boot.mounts","span":"`+spanB+`","parent":"`+spanL+`"}`,
			`{"t":1700000004000000000,"k":"end","span":"`+spanB+`"}`,
			`{"t":1700000006000000000,"k":"mark","name":"session.exec"}`,
			`{"t":1700000007000000000,"k":"mark","name":"session.exec"}`),
	})
	j := lane(t, tr, JailFile)
	if !j.Jail || j.Name != JailLane || j.Dropped != 0 {
		t.Fatalf("jail lane = %+v", j)
	}
	sum := Summarize(tr, Options{})
	if sum.JailMissing || !sum.WindowEnded {
		t.Fatalf("JailMissing=%v WindowEnded=%v", sum.JailMissing, sum.WindowEnded)
	}
	// starts at the wrapper's start, not the jail's skewed mark; ends at the first session.exec
	if sum.WindowStart != 1700000000000000000 || sum.Window() != 6*time.Second {
		t.Errorf("window [%d, %d]", sum.WindowStart, sum.WindowEnd)
	}
	// launch is clipped to the window: [1,6] less the boot step [3,4] = 4 s
	if sum.Top[0].Span.Name != "launch" || sum.Top[0].Own != 4*time.Second {
		t.Errorf("top = %s %v", sum.Top[0].Span.Name, sum.Top[0].Own)
	}
	// the trace's root between the wrapper and launch, then launch's own time either side of the
	// boot step, the second clipped at the window's end
	var gaps []string
	for _, g := range sum.Gaps {
		within := "<root>"
		if g.Within != nil {
			within = g.Within.Name
		}
		gaps = append(gaps, fmt.Sprintf("%s:%s|%s:%v", within, g.Before, g.After, g.Duration()))
	}
	want := []string{
		"<root>:process.wrapper|launch:500ms",
		"launch:|container.boot.mounts:2s",
		"launch:container.boot.mounts|:2s",
	}
	if fmt.Sprint(gaps) != fmt.Sprint(want) {
		t.Errorf("gaps = %q, want %q", gaps, want)
	}

	noJail := read(t, map[string]string{"launcher-100.jsonl": lines(launcherHeader,
		`{"t":1700000000100000000,"k":"record","name":"process.wrapper","span":"`+spanA+`","dur":100000000}`,
		`{"t":1700000000500000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
		`{"t":1700000001000000000,"k":"end","span":"`+spanL+`"}`)})
	s2 := Summarize(noJail, Options{})
	if !s2.JailMissing {
		t.Error("no jail.jsonl, yet the jail half is not missing")
	}
	// the trace's root has a 0.4 s stretch no top-level span covers
	if len(s2.Gaps) != 1 || s2.Gaps[0].Within != nil || s2.Gaps[0].Before != "process.wrapper" ||
		s2.Gaps[0].After != "launch" || s2.Gaps[0].Duration() != 400*time.Millisecond {
		t.Errorf("root gaps = %+v", s2.Gaps)
	}
}

func TestAJailFileWhoseFirstLineIsBadIsSkipped(t *testing.T) {
	tr := read(t, map[string]string{
		"launcher-100.jsonl": lines(launcherHeader, `{"t":1700000000000000000,"k":"mark","name":"x"}`),
		JailFile:             lines(`<script>`, `{"t":1700000000000000000,"k":"mark","name":"x"}`),
	})
	if len(tr.Lanes) != 1 || len(tr.Skipped) != 1 || filepath.Base(tr.Skipped[0].Path) != JailFile {
		t.Fatalf("lanes %d skipped %v", len(tr.Lanes), tr.Skipped)
	}
	if !Summarize(tr, Options{}).JailMissing {
		t.Error("JailMissing false")
	}
}

func TestLinksAndNonRegularFilesAreNotRead(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(lines(launcherHeader)), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeTrace(t, root, tid, map[string]string{"launcher-100.jsonl": lines(launcherHeader)})
	if err := os.Symlink(outside, filepath.Join(dir, "linked-1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, MetaFile)); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo-2.jsonl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir-3.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Lanes) != 1 || tr.Meta != nil {
		t.Fatalf("lanes %d meta %v", len(tr.Lanes), tr.Meta)
	}
	got := map[string]bool{}
	for _, s := range tr.Skipped {
		got[filepath.Base(s.Path)] = true
	}
	for _, f := range []string{"linked-1.jsonl", MetaFile, "fifo-2.jsonl", "dir-3.jsonl"} {
		if !got[f] {
			t.Errorf("%s not named as skipped: %v", f, tr.Skipped)
		}
	}
}

func TestExplicitParentCyclesAreRefused(t *testing.T) {
	tr := read(t, map[string]string{
		"launcher-100.jsonl": lines(launcherHeader,
			`{"t":1700000001000000000,"k":"start","name":"a","span":"`+spanA+`","parent":"`+spanB+`"}`,
			`{"t":1700000002000000000,"k":"end","span":"`+spanA+`"}`),
		"keeper-200.jsonl": lines(`{"k":"lane","name":"keeper","pid":200,"trace":"`+tid+`","parent":"`+spanA+`"}`,
			`{"t":1700000001000000000,"k":"start","name":"b","span":"`+spanB+`","parent":"`+spanA+`"}`,
			`{"t":1700000002000000000,"k":"end","span":"`+spanB+`"}`,
			`{"t":1700000001000000000,"k":"start","name":"c","span":"`+spanC+`"}`,
			`{"t":1700000001500000000,"k":"end","span":"`+spanC+`"}`),
		"svc-300.jsonl": lines(`{"k":"lane","name":"svc","pid":300,"trace":"`+tid+`","parent":"`+spanE+`"}`,
			`{"t":1700000001000000000,"k":"start","name":"d","span":"`+spanD+`"}`,
			`{"t":1700000002000000000,"k":"end","span":"`+spanD+`"}`),
		"svc2-400.jsonl": lines(`{"k":"lane","name":"svc2","pid":400,"trace":"`+tid+`","parent":"`+spanD+`"}`,
			`{"t":1700000001000000000,"k":"start","name":"e","span":"`+spanE+`"}`,
			`{"t":1700000002000000000,"k":"end","span":"`+spanE+`"}`),
	})
	for _, s := range tr.Spans {
		seen := map[*Span]bool{}
		for cur := s; cur != nil; cur = parentOf(cur) {
			if seen[cur] {
				t.Fatalf("cycle through %s", s.Name)
			}
			seen[cur] = true
		}
	}
	if len(tr.Anomalies) < 2 {
		t.Errorf("anomalies = %q", tr.Anomalies)
	}
	Summarize(tr, Options{}) // terminates
}

func TestASpanIDUsedInTwoFilesResolvesToTheFirst(t *testing.T) {
	tr := read(t, map[string]string{
		"a-1.jsonl": lines(`{"k":"lane","name":"a","pid":1,"trace":"`+tid+`"}`,
			`{"t":1700000001000000000,"k":"record","name":"first","span":"`+spanA+`","dur":1}`),
		"b-2.jsonl": lines(`{"k":"lane","name":"b","pid":2,"trace":"`+tid+`"}`,
			`{"t":1700000001000000000,"k":"record","name":"second","span":"`+spanA+`","dur":1}`,
			`{"t":1700000001000000000,"k":"record","name":"child","span":"`+spanB+`","dur":1,"parent":"`+spanA+`"}`),
	})
	if p := parentName(span(t, tr, spanB)); p != "first" || len(tr.Anomalies) != 1 {
		t.Errorf("child under %s; anomalies %q", p, tr.Anomalies)
	}
}

func TestAttributesAreCappedAndCleaned(t *testing.T) {
	var attrs []string
	for i := 0; i < 10; i++ {
		attrs = append(attrs, fmt.Sprintf(`"k%d":"v"`, i))
	}
	attrs = append(attrs, `"k0x":"`+strings.Repeat("y", 300)+`\u001b"`)
	tr := read(t, map[string]string{"launcher-100.jsonl": lines(launcherHeader,
		`{"t":1700000001000000000,"k":"record","name":"r","span":"`+spanA+`","dur":1,"attrs":{`+strings.Join(attrs, ",")+`}}`)})
	a := span(t, tr, spanA).Attrs
	if len(a) != MaxAttrs || len(a["k0x"]) != MaxAttrValue || strings.ContainsRune(a["k0x"], 0x1b) {
		t.Errorf("attrs = %d keys, k0x %d bytes", len(a), len(a["k0x"]))
	}
}

func TestMetaIsReadAndOneForAnotherTraceIgnored(t *testing.T) {
	good := read(t, map[string]string{
		MetaFile:             `{"trace":"` + tid + `","kind":"launch","start":1700000000000000000,"workspace":"ab12","unknown":1}`,
		"launcher-100.jsonl": lines(launcherHeader),
	})
	if good.Meta == nil || good.Kind() != "launch" || good.Meta.Workspace != "ab12" {
		t.Errorf("meta = %+v", good.Meta)
	}
	bad := read(t, map[string]string{MetaFile: `{"trace":"` + tidB + `","kind":"launch"}`})
	if bad.Meta != nil || len(bad.Skipped) != 1 || bad.Skipped[0].Reason != "names another trace" {
		t.Errorf("meta = %+v skipped %v", bad.Meta, bad.Skipped)
	}
}

func TestReadDirRefusesADirectoryThatIsNotATrace(t *testing.T) {
	if _, err := ReadDir(t.TempDir()); err == nil {
		t.Error("a directory not named by a trace id was read")
	}
}

// TestOverlappingTraces: a second trace whose spans fall inside the startup window is listed, one
// outside it is not, and the trace itself never is.
func TestOverlappingTraces(t *testing.T) {
	root := t.TempDir()
	writeTrace(t, root, tid, map[string]string{
		"launcher-100.jsonl": lines(launcherHeader,
			`{"t":1700000010000000000,"k":"start","name":"launch","span":"`+spanL+`"}`,
			`{"t":1700000020000000000,"k":"end","span":"`+spanL+`"}`),
		JailFile: lines(`{"t":1700000015000000000,"k":"mark","name":"session.exec"}`),
	})
	writeTrace(t, root, tidB, map[string]string{
		MetaFile: `{"trace":"` + tidB + `","kind":"preparation"}`,
		"scheduler-7.jsonl": lines(`{"k":"lane","name":"scheduler","pid":7,"trace":"`+tidB+`"}`,
			`{"t":1700000014000000000,"k":"record","name":"preparation.build","span":"`+spanA+`","dur":5000000000}`),
	})
	writeTrace(t, root, tidC, map[string]string{
		"scheduler-8.jsonl": lines(`{"k":"lane","name":"scheduler","pid":8,"trace":"`+tidC+`"}`,
			// after the window's end at session.exec, though inside the trace's own extent
			`{"t":1700000019000000000,"k":"record","name":"preparation.check","span":"`+spanA+`","dur":3000000000}`),
	})
	if err := os.Mkdir(filepath.Join(root, "not-a-trace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, tidB), filepath.Join(root, "ffffffffffffffffffffffffffffffff")); err != nil {
		t.Fatal(err)
	}
	traces, skipped, err := ReadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 3 || len(skipped) != 1 || !strings.HasSuffix(skipped[0].Path, "ffffffffffffffffffffffffffffffff") {
		t.Fatalf("traces %d skipped %v", len(traces), skipped)
	}
	var self *Trace
	for _, tr := range traces {
		if tr.ID == tid {
			self = tr
		}
	}
	sum := Summarize(self, Options{Others: traces})
	if len(sum.Overlapping) != 1 || sum.Overlapping[0].Trace.ID != tidB || sum.Overlapping[0].Trace.Kind() != "preparation" {
		var ids []string
		for _, o := range sum.Overlapping {
			ids = append(ids, o.Trace.ID)
		}
		t.Errorf("overlapping = %v", ids)
	}
}

func TestTopIsCappedAndRanked(t *testing.T) {
	var ls []string
	ls = append(ls, launcherHeader)
	for i := 1; i <= 20; i++ {
		ls = append(ls, fmt.Sprintf(`{"t":%d,"k":"record","name":"s%02d","span":"%016x","dur":%d}`,
			1700000000000000000+int64(i)*100_000_000_000, i, i, int64(i)*1_000_000))
	}
	sum := Summarize(read(t, map[string]string{"launcher-100.jsonl": lines(ls...)}), Options{})
	if len(sum.Top) != DefaultTop || sum.Top[0].Span.Name != "s20" || sum.Top[DefaultTop-1].Span.Name != "s06" {
		t.Errorf("top = %d, first %s", len(sum.Top), sum.Top[0].Span.Name)
	}
}
