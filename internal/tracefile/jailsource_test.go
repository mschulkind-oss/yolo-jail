package tracefile

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func jailSourceTestID(n byte) (id JailSourceInstance) {
	id[15] = n
	return id
}

func jailSourceTestInput(data string, id byte) JailSourceInput {
	return JailSourceInput{
		Bytes:            []byte(data),
		SourceInstance:   jailSourceTestID(id),
		FirstRowComplete: true,
	}
}

func TestJailSourceParsesGoAndShellProducerRows(t *testing.T) {
	input := jailSourceTestInput(
		`{"t":1700000000000000001,"k":"start","name":"Daemon-Ready","pid":41}`+"\n"+
			`{"t":1700000000123456000,"k":"mark","name":"program.setup-step","pid":42}`+"\n"+
			`{"t":1700000000,"k":"end","name":"program.Claude-Code","pid":43,"res":"s"}`+"\n", 1)
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 3 {
		t.Fatalf("observations = %d, want 3; malformed=%d", len(got.Observations), got.MalformedRows)
	}
	goRow, shellHighResolution, shellCoarse := got.Observations[0], got.Observations[1], got.Observations[2]
	if goRow.Time != 1700000000000000001 || goRow.Kind != "start" || goRow.Name != "daemon_ready" || goRow.PID != 41 || goRow.CoarseSeconds {
		t.Errorf("Go producer row = %+v", goRow)
	}
	if shellHighResolution.Time != 1700000000123456000 || shellHighResolution.Kind != "mark" || shellHighResolution.Name != "program.setup_step" || shellHighResolution.PID != 42 || shellHighResolution.CoarseSeconds {
		t.Errorf("shell EPOCHREALTIME row = %+v", shellHighResolution)
	}
	if shellCoarse.Time != 1700000000000000000 || shellCoarse.Kind != "end" || shellCoarse.Name != "program.claude_code" || shellCoarse.PID != 43 || !shellCoarse.CoarseSeconds {
		t.Errorf("shell whole-second row = %+v", shellCoarse)
	}
}

func TestJailSourceDecodesEscapesAndRejectsInvalidJSON(t *testing.T) {
	input := jailSourceTestInput(
		`{"t":1,"k":"mark","name":"F\u006fo","pid":1}`+"\n"+
			`{"t":2,"k":"mark","name":"bad","pid":1`+"\n", 2)
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.MalformedRows != 1 {
		t.Fatalf("parse result = %+v", got)
	}
	if got.Observations[0].Name != "foo" {
		t.Fatalf("escaped display name = %q", got.Observations[0].Name)
	}
	if JailSourceNamesEqual(got.Observations[0], ParseJailSource(jailSourceTestInput(
		`{"t":1,"k":"mark","name":"Foo","pid":1}`+"\n", 3), NewJailSourceBudget()).Observations[0]) == false {
		t.Fatal("escaped producer spelling did not decode to the same exact name")
	}
	invalidUTF8 := jailSourceTestInput("{\"t\":1,\"k\":\"mark\",\"name\":\"\xff\",\"pid\":1}\n", 4)
	if got := ParseJailSource(invalidUTF8, NewJailSourceBudget()); len(got.Observations) != 0 || got.MalformedRows != 1 {
		t.Fatalf("invalid UTF-8 row = %+v", got)
	}
}

func TestJailSourceRequiresExactRepresentableIntegralTimeAndPID(t *testing.T) {
	rows := []string{
		`{"t":1.5,"k":"mark","name":"x","pid":1}`,
		`{"t":9223372036854775808,"k":"mark","name":"x","pid":1}`,
		`{"t":1,"k":"mark","name":"x","pid":1.5}`,
		`{"t":1,"k":"mark","name":"x","pid":9223372036854775808}`,
		`{"t":9223372037,"k":"mark","name":"x","pid":1,"res":"s"}`,
		`{"t":-9223372037,"k":"mark","name":"x","pid":1,"res":"s"}`,
	}
	input := jailSourceTestInput(strings.Join(rows, "\n")+"\n", 5)
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 0 || got.MalformedRows != len(rows) {
		t.Fatalf("nonintegral/overflow rows = %+v", got)
	}
	integralJSONNumbers := jailSourceTestInput(
		`{"t":1.0,"k":"mark","name":"x","pid":1e0}`+"\n", 16)
	got = ParseJailSource(integralJSONNumbers, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.Observations[0].Time != 1 || got.Observations[0].PID != 1 {
		t.Fatalf("exact integral JSON number = %+v", got)
	}
}

func TestJailSourceIgnoresUnknownSensitiveFieldsAndOmitsPrivateMatchNameFromJSON(t *testing.T) {
	input := jailSourceTestInput(
		`{"t":123,"k":"mark","name":"Known","pid":7,"id":"secret-id","parent":"secret-parent","path":"/private/path","credential":"secret-token"}`+"\n", 6)
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.MalformedRows != 0 {
		t.Fatalf("parse result = %+v", got)
	}
	encoded, err := json.Marshal(got.Observations[0])
	if err != nil {
		t.Fatal(err)
	}
	matchingName, eligible := got.Observations[0].PrivateMatchingName()
	if !eligible || matchingName != "Known" {
		t.Fatalf("private matching evidence eligibility or exact spelling failed: eligible=%v, exact=%v", eligible, matchingName == "Known")
	}
	debug := fmt.Sprintf("%+v %#v", got.Observations[0], got.Observations[0])
	if strings.Contains(debug, matchingName) {
		t.Fatalf("debug formatting exposed private matching evidence: %s", debug)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inputJSON), "secret-token") || strings.Contains(string(inputJSON), "credential") {
		t.Fatalf("serialized parser input exposed source bytes: %s", inputJSON)
	}
	inputDebug := fmt.Sprintf("%+v %#v", input, input)
	if strings.Contains(inputDebug, "secret-token") || strings.Contains(inputDebug, "credential") {
		t.Fatalf("debug formatting exposed source bytes: %s", inputDebug)
	}
	for _, forbidden := range []string{"secret-id", "secret-parent", "/private/path", "secret-token", "Known"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("serialized observation contains private value %q: %s", forbidden, encoded)
		}
	}
	for _, forbidden := range []string{`"id"`, `"parent"`, `"path"`, `"credential"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("serialized observation contains unknown field %s: %s", forbidden, encoded)
		}
	}
}

func TestJailSourceRejectsMalformedCompleteRows(t *testing.T) {
	input := jailSourceTestInput(strings.Join([]string{
		`null`, `[]`, `{"t":1,"k":"other","name":"x","pid":1}`,
		`{"t":1,"k":"mark","name":"x","pid":1,"res":"ms"}`,
		`{"t":1,"k":"mark","name":"x","pid":1,"t":2}`,
		`{"t":1,"k":"mark","name":"x"}`,
	}, "\n")+"\n", 7)
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 0 || got.MalformedRows != 6 {
		t.Fatalf("malformed rows = %+v", got)
	}
}

func TestJailSourceOffsetsAndIdenticalRowsAreDistinct(t *testing.T) {
	row := `{"t":123,"k":"mark","name":"x","pid":7}`
	input := jailSourceTestInput(row+"\n"+row+"\n", 8)
	input.AbsoluteOffset = 500
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 2 {
		t.Fatalf("observations = %+v", got.Observations)
	}
	if got.Observations[0].Offset != 500 || got.Observations[1].Offset != 500+int64(len(row)+1) {
		t.Fatalf("absolute offsets = %d, %d", got.Observations[0].Offset, got.Observations[1].Offset)
	}
}

func TestJailSourceBoundsTailBytesAndCompletePhysicalRows(t *testing.T) {
	prefix := strings.Repeat("x", JailSourceMaxBytes+17)
	row := `{"t":1,"k":"mark","name":"tail","pid":1}`
	data := prefix + "\n" + row + "\n"
	got := ParseJailSource(JailSourceInput{Bytes: []byte(data), FirstRowComplete: true, SourceInstance: jailSourceTestID(9)}, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.Observations[0].Offset != int64(len(prefix)+1) || !got.TruncatedBytes {
		t.Fatalf("tail-byte result = %+v", got)
	}

	// A tail that starts exactly after a newline has a complete first row and keeps it.
	rowBoundary := strings.Repeat("x", 9) + "\n"
	tailPadding := strings.Repeat("z", JailSourceMaxBytes-len(row)-1)
	data = rowBoundary + row + "\n" + tailPadding
	got = ParseJailSource(JailSourceInput{Bytes: []byte(data), FirstRowComplete: true, SourceInstance: jailSourceTestID(18)}, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.Observations[0].Offset != int64(len(rowBoundary)) || !got.TruncatedBytes {
		t.Fatalf("boundary-aligned tail row = %+v", got)
	}

	row = `{"t":1,"k":"mark","name":"x","pid":1}`
	rows := []string{row, row, row, `null`, `null`}
	for len(rows) < JailSourceMaxRows+3 {
		rows = append(rows, row)
	}
	got = ParseJailSource(jailSourceTestInput(strings.Join(rows, "\n")+"\n", 10), NewJailSourceBudget())
	if len(got.Observations) != JailSourceMaxRows-2 || got.MalformedRows != 2 || !got.TruncatedRows {
		t.Fatalf("row-bound result observations=%d malformed=%d truncated=%v", len(got.Observations), got.MalformedRows, got.TruncatedRows)
	}
	wantOffset := int64(3*len(row) + 2*len("null") + 5)
	if got.Observations[0].Offset != wantOffset {
		t.Fatalf("first retained valid offset = %d, want %d", got.Observations[0].Offset, wantOffset)
	}
}

func TestJailSourceSkipsPartialFirstAndIncompleteFinalRows(t *testing.T) {
	row := `{"t":1,"k":"mark","name":"whole","pid":1}`
	input := JailSourceInput{
		Bytes:            []byte("partial-prefix\n" + row + "\n" + row),
		AbsoluteOffset:   80,
		SourceInstance:   jailSourceTestID(11),
		FirstRowComplete: false,
	}
	got := ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.Observations[0].Offset != 80+int64(len("partial-prefix\n")) || got.MalformedRows != 0 {
		t.Fatalf("partial first/incomplete final rows: %+v", got)
	}
	input.Bytes = []byte(row + "\n" + row)
	input.AbsoluteOffset = 0
	input.FirstRowComplete = true
	got = ParseJailSource(input, NewJailSourceBudget())
	if len(got.Observations) != 1 || got.Observations[0].Offset != 0 {
		t.Fatalf("complete first/final rows = %+v", got.Observations)
	}
}

func TestJailSourceLifetimeBudgetCountsDistinctSourceOccurrences(t *testing.T) {
	row := `{"t":1,"k":"mark","name":"x","pid":1}`
	data := row + "\n" + row + "\n"
	budget := NewJailSourceBudgetWithLimit(2)
	first := jailSourceTestInput(data, 12)
	got := ParseJailSource(first, budget)
	if len(got.Observations) != 2 || budget.Remaining() != 0 {
		t.Fatalf("initial admission: observations=%d remaining=%d", len(got.Observations), budget.Remaining())
	}
	got = ParseJailSource(first, budget)
	if len(got.Observations) != 2 || got.BudgetExhausted || budget.Remaining() != 0 {
		t.Fatalf("reread admission: %+v remaining=%d", got, budget.Remaining())
	}
	second := jailSourceTestInput(data, 13)
	got = ParseJailSource(second, budget)
	if len(got.Observations) != 0 || !got.BudgetExhausted || budget.Remaining() != 0 {
		t.Fatalf("new source after exhaustion: %+v remaining=%d", got, budget.Remaining())
	}
}

func TestJailSourceDefaultLifetimeBudgetStopsAtTwoThousandDistinctOccurrences(t *testing.T) {
	row := `{"t":1,"k":"mark","name":"x","pid":1}`
	rows := strings.TrimSuffix(strings.Repeat(row+"\n", JailSourceMaxOccurrences), "\n") + "\n"
	budget := NewJailSourceBudget()
	got := ParseJailSource(jailSourceTestInput(rows, 17), budget)
	if len(got.Observations) != JailSourceMaxOccurrences || budget.Admitted() != JailSourceMaxOccurrences || budget.Remaining() != 0 {
		t.Fatalf("full lifetime admission: observations=%d admitted=%d remaining=%d", len(got.Observations), budget.Admitted(), budget.Remaining())
	}
	got = ParseJailSource(jailSourceTestInput(row+"\n", 18), budget)
	if len(got.Observations) != 0 || !got.BudgetExhausted || budget.Admitted() != JailSourceMaxOccurrences {
		t.Fatalf("post-limit admission: %+v admitted=%d", got, budget.Admitted())
	}
}

func TestJailSourceDisplayNamesDoNotCollideWithPrivateMatchingNames(t *testing.T) {
	rows := []string{
		`{"t":1,"k":"mark","name":"Foo","pid":1}`,
		`{"t":1,"k":"mark","name":"foo","pid":1}`,
		`{"t":1,"k":"mark","name":"foo-A","pid":1}`,
		`{"t":1,"k":"mark","name":"foo_A","pid":1}`,
		`{"t":1,"k":"mark","name":"foo/","pid":1}`,
		`{"t":1,"k":"mark","name":"foo_","pid":1}`,
		`{"t":1,"k":"mark","name":"","pid":1}`,
	}
	longPrefix := strings.Repeat("a", 64)
	rows = append(rows,
		`{"t":1,"k":"mark","name":"`+longPrefix+`x","pid":1}`,
		`{"t":1,"k":"mark","name":"`+longPrefix+`y","pid":1}`,
	)
	got := ParseJailSource(jailSourceTestInput(strings.Join(rows, "\n")+"\n", 14), NewJailSourceBudget())
	if len(got.Observations) != len(rows) {
		t.Fatalf("observations = %d, want %d", len(got.Observations), len(rows))
	}
	obs := got.Observations
	for i, want := range []string{"foo", "foo", "foo_a", "foo_a", "foo_", "foo_", "", longPrefix, longPrefix} {
		if obs[i].Name != want {
			t.Errorf("display name %d = %q, want %q", i, obs[i].Name, want)
		}
	}
	if JailSourceNamesEqual(obs[0], obs[1]) || JailSourceNamesEqual(obs[2], obs[3]) || JailSourceNamesEqual(obs[4], obs[5]) {
		t.Fatal("normalization collisions were treated as matching evidence")
	}
	if !JailSourceNamesEqual(obs[6], obs[6]) {
		t.Fatal("eligible empty producer name did not compare exactly")
	}
	if JailSourceNamesEqual(obs[7], obs[8]) {
		t.Fatal("overlength spellings sharing a display prefix became equivalent")
	}
}

func TestJailSourceMatchingEvidenceRequiresProducerAlphabetAndLength(t *testing.T) {
	rows := []string{
		`{"t":1,"k":"mark","name":"legal.Name-1","pid":1}`,
		`{"t":1,"k":"mark","name":"legal/Name-1","pid":1}`,
		`{"t":1,"k":"mark","name":"` + strings.Repeat("z", 65) + `","pid":1}`,
	}
	got := ParseJailSource(jailSourceTestInput(strings.Join(rows, "\n")+"\n", 15), NewJailSourceBudget())
	if len(got.Observations) != len(rows) {
		t.Fatalf("observations = %d", len(got.Observations))
	}
	if !JailSourceNamesEqual(got.Observations[0], got.Observations[0]) {
		t.Fatal("valid producer alphabet was ineligible")
	}
	if JailSourceNamesEqual(got.Observations[1], got.Observations[1]) || JailSourceNamesEqual(got.Observations[2], got.Observations[2]) {
		t.Fatal("ineligible name evidence matched itself")
	}
}
