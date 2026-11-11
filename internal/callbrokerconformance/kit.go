// Package callbrokerconformance supplies test assertions over adapter observations.
// It owns no classifier, service, credential or approval authority.
package callbrokerconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
)

type Call struct {
	Argv  []string
	Stdin []byte
}
type ClassificationCase struct {
	Name          string
	Call          Call
	Want          json.RawMessage
	ErrorContains string
}
type Classify func(context.Context, Call) (json.RawMessage, error)

func CheckClassifications(ctx context.Context, classify Classify, cases []ClassificationCase) error {
	if classify == nil {
		return fmt.Errorf("classification table: nil classifier")
	}
	if len(cases) == 0 {
		return fmt.Errorf("classification table: empty")
	}
	for i, row := range cases {
		name := row.Name
		if name == "" {
			return fmt.Errorf("classification row %d: missing name", i)
		}
		hasWant := len(row.Want) != 0
		if hasWant == (row.ErrorContains != "") {
			return fmt.Errorf("%s: require exactly one JSON or error expectation", name)
		}
		var want any
		if hasWant {
			var err error
			want, err = jsonValue(row.Want)
			if err != nil {
				return fmt.Errorf("%s: invalid expected JSON: %w", name, err)
			}
		}
		// Both invocations are mandatory, including negative core-validation cases.
		first, err1 := classify(ctx, Call{Argv: append([]string(nil), row.Call.Argv...), Stdin: append([]byte(nil), row.Call.Stdin...)})
		second, err2 := classify(ctx, Call{Argv: append([]string(nil), row.Call.Argv...), Stdin: append([]byte(nil), row.Call.Stdin...)})
		if !hasWant {
			for n, err := range []error{err1, err2} {
				if err == nil || !strings.Contains(err.Error(), row.ErrorContains) {
					return fmt.Errorf("%s: invocation %d: expected error containing %q, got %v", name, n+1, row.ErrorContains, err)
				}
			}
			if err1.Error() != err2.Error() {
				return fmt.Errorf("%s: nondeterministic errors", name)
			}
			continue
		}
		var values [2]any
		for n, raw := range []json.RawMessage{first, second} {
			err := []error{err1, err2}[n]
			if err != nil {
				return fmt.Errorf("%s: invocation %d: unexpected error: %w", name, n+1, err)
			}
			value, err := jsonValue(raw)
			if err != nil {
				return fmt.Errorf("%s: invocation %d: invalid answer JSON: %w", name, n+1, err)
			}
			values[n] = value
		}
		if !reflect.DeepEqual(values[0], values[1]) {
			return fmt.Errorf("%s: nondeterministic answers", name)
		}
		if !reflect.DeepEqual(values[0], want) {
			return fmt.Errorf("%s: answer differs from expected JSON: got %s, want %s", name, first, row.Want)
		}
	}
	return nil
}

type Counts struct{ Writes, Pending, GrantUses int }
type Observation struct {
	Verdict, Output string
	Counts          Counts
}
type Step struct {
	Name                           string
	Run                            func(context.Context) (Observation, error)
	WantVerdict                    string
	WantCounts                     Counts
	ErrorContains                  string
	OutputContains, OutputExcludes []string
}

func CheckSteps(ctx context.Context, steps []Step) error {
	if len(steps) == 0 {
		return fmt.Errorf("execution script: empty")
	}
	for i, s := range steps {
		if s.Name == "" {
			return fmt.Errorf("execution step %d: missing name", i)
		}
		if s.Run == nil {
			return fmt.Errorf("%s: nil action", s.Name)
		}
		if s.WantVerdict == "" {
			return fmt.Errorf("%s: missing expected verdict", s.Name)
		}
		if s.ErrorContains != "" && (s.WantVerdict == "runs" || s.WantVerdict == "approved") {
			return fmt.Errorf("%s: success verdict and error expectations are ambiguous", s.Name)
		}
		got, err := s.Run(ctx)
		if s.ErrorContains == "" {
			if err != nil {
				return fmt.Errorf("%s: unexpected error: %w", s.Name, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), s.ErrorContains) {
			return fmt.Errorf("%s: expected error containing %q, got %v", s.Name, s.ErrorContains, err)
		}
		if got.Verdict != s.WantVerdict {
			return fmt.Errorf("%s: verdict %q, want %q", s.Name, got.Verdict, s.WantVerdict)
		}
		if got.Counts != s.WantCounts {
			return fmt.Errorf("%s: counts %+v, want %+v", s.Name, got.Counts, s.WantCounts)
		}
		for _, text := range s.OutputContains {
			if !strings.Contains(got.Output, text) {
				return fmt.Errorf("%s: output missing %q", s.Name, text)
			}
		}
		for _, text := range s.OutputExcludes {
			if strings.Contains(got.Output, text) {
				return fmt.Errorf("%s: output contains forbidden %q", s.Name, text)
			}
		}
	}
	return nil
}

// JSON numbers are compared exactly, without float64 rounding or spelling differences.
func jsonValue(raw json.RawMessage) (any, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(value), nil
}

type exactJSONNumber string

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		return normalizeJSONNumber(string(v))
	case []any:
		for i, item := range v {
			v[i] = normalizeJSONNumbers(item)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = normalizeJSONNumbers(item)
		}
	}
	return value
}

func normalizeJSONNumber(number string) exactJSONNumber {
	// Normalize significand and exponent as text; a huge exponent must not allocate
	// its expanded decimal value. json.Valid has already checked the syntax.
	exponent := new(big.Int)
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		exponent.SetString(number[i+1:], 10)
		number = number[:i]
	}
	negative := strings.HasPrefix(number, "-")
	number = strings.TrimPrefix(number, "-")
	if i := strings.IndexByte(number, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(number)-i-1)))
		number = number[:i] + number[i+1:]
	}
	number = strings.TrimLeft(number, "0")
	if number == "" {
		return "0"
	}
	trimmed := strings.TrimRight(number, "0")
	exponent.Add(exponent, big.NewInt(int64(len(number)-len(trimmed))))
	if negative {
		trimmed = "-" + trimmed
	}
	return exactJSONNumber(trimmed + "e" + exponent.String())
}
