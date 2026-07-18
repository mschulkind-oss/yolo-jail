package json5

import "testing"

// FuzzDecode checks that Decode never PANICS on arbitrary input (a crash is a
// bug; a returned error is fine). Keep -fuzztime SMALL in CI (this is a
// robustness guard, not an exhaustive search) — the parity test is the real
// correctness gate. Run: `go test -run x -fuzz FuzzDecode -fuzztime=20s`.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		`{}`, `[]`, `{"a":1}`, `// c\n{}`, `{a:1,}`, `{'s':'q'}`, `0xff`,
		`.5`, `5.`, `Infinity`, `NaN`, `"😀"`, `{`, `[1 2]`, ``,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic. Result (value or error) is unchecked here.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decode panicked on %q: %v", data, r)
			}
		}()
		_, _ = Decode(data)
	})
}
