package callbroker

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// The process boundary checks duplicate fields before this same strict decoder.
// Seed execution is part of the short package gate; fuzzing never invokes a tool.
func FuzzScopedClassificationStrictJSON(f *testing.F) {
	f.Add([]byte(negotiationRead(legacyClassifierVersion, false, "", "team:alpha")), false)
	f.Add([]byte(negotiationRead(typedClassifierVersion, true, "list", "team:alpha")), true)
	f.Add([]byte(`{"version":"yolo.call-broker-classifier/1","version":"bad","local":true}`), false)
	f.Add([]byte(`{"version":"yolo.call-broker-classifier/2","classification":{"argv":["list"],"stdin_sha256":"bad","reads":[{"cell":null,"chain":[]}]}}`), true)
	f.Fuzz(func(t *testing.T, raw []byte, typed bool) {
		if len(raw) > classifierLimit || rejectDuplicateJSONFields(raw) != nil {
			return
		}
		var answer Answer
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&answer) != nil || decoder.Decode(new(any)) != io.EOF {
			return
		}
		classifier := Classifier{Source: "fixture", Roots: []string{"team:alpha"}, Resources: []loopholedecl.CallBrokerResource{{Type: "project"}}, Cells: explicitReadCells(), ReadGrant: true}
		version := legacyClassifierVersion
		if typed {
			version = typedClassifierVersion
		}
		input := CLIInput{Version: version, Argv: []string{"list"}}
		if answer.Classification != nil {
			input.StdinSHA256 = answer.Classification.StdinSHA256
		}
		if err := classifier.validate(answer, input); err != nil || answer.Classification == nil {
			return
		}
		for _, operation := range answer.Classification.Operations {
			if operation.Source != classifier.Source || len(operation.Chain) == 0 {
				t.Fatal("strict decoder returned an invalid derived operation")
			}
		}
	})
}

func FuzzHTTPScopedClassificationShape(f *testing.F) {
	f.Add([]byte(fixtureHTTPAnswer(false, digestHTTPBody(false, nil), "", "original")))
	f.Add([]byte(`{"version":"yolo.call-broker-classifier/2","classification":{"canonical":{},"reads":[{"cell":"x","cell":"y","chain":[]}]}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > classifierLimit || rejectDuplicateJSONFields(raw) != nil || validateHTTPAnswerShape(raw) != nil {
			return
		}
		var answer httpClassifierAnswer
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&answer) != nil {
			return
		}
		if answer.Classification != nil {
			classifier := Classifier{Source: "fixture", Roots: []string{"team:alpha"}, Resources: []loopholedecl.CallBrokerResource{{Type: "project"}, {Type: "task", Parent: "project"}}, Cells: explicitReadCells(), ReadGrant: true}
			_, _, _, _ = classifier.validateHTTPClassification(fixtureHTTPCall(nil, false), []string{"content-type"}, answer.Classification, typedClassifierVersion)
		}
	})
}
