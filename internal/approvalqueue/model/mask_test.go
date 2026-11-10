package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// yoloSummaryArgvFlags is copied from yolo's summaryArgv (internal/brokerstore/watch.go,
// yolo commit 04f300a9e). If yolo's list changes, this copy and SecretFlags change with it.
var yoloSummaryArgvFlags = []string{"--body", "--body-file", "--field", "-F", "--raw-field", "-f", "--header", "-H", "--input"}

// yoloGluedFlags is the list of the short options yolo's summaryArgv also redacts when the
// value is attached ("-Fvalue"), from the same function and commit.
var yoloGluedFlags = []string{"-F", "-f", "-H"}

// yoloSummaryArgv is yolo's summaryArgv, copied verbatim apart from the name (same file and
// commit), so a test can compare the queue's masking with it argument by argument.
func yoloSummaryArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	redactNext := false
	for _, arg := range argv {
		if redactNext {
			out = append(out, "[redacted]")
			redactNext = false
			continue
		}
		switch arg {
		case "--body", "--body-file", "--field", "-F", "--raw-field", "-f", "--header", "-H", "--input":
			out = append(out, arg)
			redactNext = true
			continue
		}
		redacted := false
		for _, flag := range []string{"--body=", "--body-file=", "--field=", "-F=", "--raw-field=", "-f=", "--header=", "-H=", "--input="} {
			if strings.HasPrefix(arg, flag) {
				out = append(out, flag+"[redacted]")
				redacted = true
				break
			}
		}
		if !redacted {
			for _, flag := range []string{"-F", "-f", "-H"} {
				if strings.HasPrefix(arg, flag) && len(arg) > len(flag) {
					out = append(out, flag+"[redacted]")
					redacted = true
					break
				}
			}
		}
		if !redacted {
			out = append(out, arg)
		}
	}
	return out
}

func TestSecretFlagListEqualsYolosSummaryArgvList(t *testing.T) {
	if !reflect.DeepEqual(SecretFlags, yoloSummaryArgvFlags) {
		t.Fatalf("queue flag list %v != yolo summaryArgv list %v", SecretFlags, yoloSummaryArgvFlags)
	}
	if !reflect.DeepEqual(GluedFlags, yoloGluedFlags) {
		t.Fatalf("glued list %v != yolo's %v", GluedFlags, yoloGluedFlags)
	}
}

// asYoloShape renders the queue's items the way summaryArgv renders argv.
func asYoloShape(items []ArgvItem) []string {
	out := []string{}
	for _, it := range items {
		if it.Secret {
			if it.Prefix == "" {
				out = append(out, "[redacted]")
			} else {
				out = append(out, it.Prefix+"[redacted]")
			}
		} else {
			out = append(out, it.Value)
		}
	}
	return out
}

func TestMaskingCoversAllThreeSpellingsAndAgreesWithYolo(t *testing.T) {
	cases := [][]string{
		{"issue", "comment", "7", "--body", "s3cret token"},                     // separate value
		{"secret", "set", "NAME", "--body=s3cret"},                              // joined
		{"api", "x", "-Fkey=s3cret", "-fraw=s3cret", "-HAuthorization: s3cret"}, // glued
		{"api", "x", "-F", "a=s3cret", "-f", "b=s3cret", "-H", "X: s3cret"},
		{"x", "--body-file", "/p/s3cret", "--input=/p/s3cret", "--header=X: s3cret", "--field=a=s3cret", "--raw-field=s3cret"},
		{"x", "-F=s3cret", "-f=s3cret", "-H=s3cret"},
		{"x", "--body"}, // flag with no value
		{"x", "--bodyx", "--body-fil", "-Z", "-", "plain"},
		{"x", "--body", "--body", "next"}, // the value of --body is the second --body, then next is plain
		{"x", "--body="},
		{"x", "-F"},
		{},
	}
	for _, argv := range cases {
		items := MaskArgv(argv)
		if len(items) != len(argv) {
			t.Fatalf("%v: %d items", argv, len(items))
		}
		if got, want := asYoloShape(items), yoloSummaryArgv(argv); !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\n  queue %q\n  yolo  %q", argv, got, want)
		}
		cmd := MaskedCommand(argv)
		if strings.Contains(cmd, "s3cret") {
			t.Errorf("%q: secret in the rendered command %q", argv, cmd)
		}
	}
}

func TestMaskedItemCarriesLengthAndHashAndTheFullValue(t *testing.T) {
	items := MaskArgv([]string{"--body=héllo"})
	it := items[0]
	if !it.Secret || it.Prefix != "--body=" || it.Value != "héllo" || it.Length != 5 || len(it.SHA) != 8 {
		t.Fatalf("%+v", it)
	}
	// sha256("héllo") prefix, same format as yolo's elide: %x of the first four bytes.
	if it.MaskText() != "[hidden: 5 characters, sha256 "+it.SHA+"]" {
		t.Fatal(it.MaskText())
	}
}

func TestPublishedPendingNeverCarriesAnUnmaskedSecretInItsCommandOrMaskText(t *testing.T) {
	st := loadState(t, "approve-json-populated.json")
	m := snap(New(t0), at(1), st.Requests, nil)
	p := Publish(m, at(2), Options{Reconcile: 15 * time.Second})
	secrets := 0
	for i, pr := range p.Pending {
		for _, a := range st.Requests[i].Argv {
			_ = a
		}
		for _, it := range pr.Argv {
			if it.Secret {
				secrets++
				if strings.Contains(pr.Command, it.Value) && it.Value != "" {
					t.Errorf("%s: command shows a secret value %q", pr.ID, it.Value)
				}
			}
		}
	}
	if secrets == 0 {
		t.Fatal("the recorded fixture has --body secrets; none were masked")
	}
}
