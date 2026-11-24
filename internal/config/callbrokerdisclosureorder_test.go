package config

import (
	"reflect"
	"testing"
)

func TestSameCallBrokerDisclosurePreservesExactDeclarationBytesAndInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous []string
		current  []string
		equal    bool
	}{
		{"record reordered", []string{"classifier=yolo", "cell change", "source aws"}, []string{"source aws", "classifier=yolo", "cell change"}, true},
		{"changed declaration", []string{"cell change"}, []string{"cell delete"}, false},
		{"changed byte case", []string{"source AWS"}, []string{"source aws"}, false},
		{"changed whitespace", []string{"cell change"}, []string{"cell change "}, false},
		{"duplicate removed", []string{"cell change", "cell change"}, []string{"cell change"}, false},
		{"duplicate reordered", []string{"b", "a", "a"}, []string{"a", "b", "a"}, true},
		{"empty declaration", nil, []string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforePrevious := append([]string(nil), tc.previous...)
			beforeCurrent := append([]string(nil), tc.current...)
			if got := sameCallBrokerDisclosure(tc.previous, tc.current); got != tc.equal {
				t.Fatalf("declaration equality=%t, want %t", got, tc.equal)
			}
			if len(tc.previous) > 0 && !reflect.DeepEqual(tc.previous, beforePrevious) || len(tc.current) > 0 && !reflect.DeepEqual(tc.current, beforeCurrent) {
				t.Fatal("declaration comparison mutated caller-owned input")
			}
		})
	}
}
