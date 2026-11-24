package brokerpolicy

import (
	"reflect"
	"strings"
	"testing"
)

func TestScopedBrokerMatching(t *testing.T) {
	anchors := []string{"s3://", "ce://"}
	m, err := NewScopeMatcher("segment", "/", anchors)
	if err != nil {
		t.Fatal(err)
	}
	anchors[0] = "changed"
	for _, tc := range []struct {
		name, scope, id string
		want            bool
	}{
		{"H1-prefix", "s3://a/team/", "s3://a/team/x", true}, {"H1-sibling", "s3://a/team/", "s3://a/team-other/x", false},
		{"H2-bucket-sibling", "s3://b", "s3://bucket2/k", false}, {"H3-anchor", "s3://", "s3://b/k", true},
		{"H4-operation", "ce://host-login", "ce://host-login/get-tags", true}, {"H12-case", "s3://Acme", "s3://acme/k", false},
		{"no-normalization", "s3://b/a/../x", "s3://b/x", false}, {"percent-bytes", "s3://b/%61", "s3://b/a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.Contains(tc.scope, tc.id); got != tc.want {
				t.Fatalf("Contains(%q,%q)=%t", tc.scope, tc.id, got)
			}
		})
	}
	got := m.ApplyingScopes([]string{"s3://", "s3://b", "s3://b/", "s3://b/reports/"}, []string{"s3://b/reports/x", "s3://b"})
	var ordered []string
	for _, match := range got {
		ordered = append(ordered, match.Scope)
	}
	if !reflect.DeepEqual(ordered, []string{"s3://b/reports/", "s3://b/", "s3://b", "s3://"}) {
		t.Fatalf("H3 ordered=%v", ordered)
	}
	for _, invalid := range []string{"", "gs://b", "s3://a\n", "s3://" + strings.Repeat("x", 1020), "s3://\xff"} {
		if err := m.ValidateScope(invalid); err == nil {
			t.Fatalf("H6 accepted %q", invalid)
		}
	}
	for _, valid := range []string{"s3://", "s3://b/../x", "s3://équipe prod/"} {
		if err := m.ValidateScope(valid); err != nil {
			t.Fatal(err)
		}
	}
	exact, err := NewScopeMatcher("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Contains("x/a", "x/a/b") {
		t.Fatal("H8 inferred opaque ancestry")
	}
	if exact.RootRelation("x/a", []string{"x/a/b"}) != "classifier-conditional" {
		t.Fatal("exact report guessed ancestry")
	}
	if m.RootRelation("s3://", []string{"s3://b/reports/"}) != "overlap" || m.RootRelation("s3://elsewhere", []string{"s3://b"}) != "inert" {
		t.Fatal("H5/H7 report relation")
	}
}
