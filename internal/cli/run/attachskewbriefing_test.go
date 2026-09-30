package run

// attachskewbriefing_test.go pins SK-D15 (docs/design/attach-skew-and-contract-guardrails.md,
// OQ-SK4): an attach that goes ahead under YOLO_ALLOW_ATTACH_SKEW names the skew in the briefing
// it refreshes, and says on stderr whether a briefing carries it; an attach that needs nothing it
// lacks writes no section, and nothing outside attachExisting's refresh can carry one.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const skewSectionHeading = "## ⚠ This session runs in a jail that could not take what started it"

// claudeBriefingOf reads the claude pack's staged briefing for cname.
func claudeBriefingOf(t *testing.T, cname string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(paths.AgentsDir(), cname, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatalf("no staged claude briefing: %v", err)
	}
	return string(b)
}

// TestAnAcknowledgedAttachNamesItsSkewInTheBriefing: a pre-gate jail (launched by v0.10.0) cannot
// take the zai profile's per-agent values; under the acknowledgment the attach goes ahead, and the
// briefing it refreshes for the session names the version the jail was launched with, the tag it
// lacks, the withheld variable names (never a value) and the host's remedy, and stderr says the
// briefing carries it. The next entry, which needs nothing the jail lacks, rewrites the briefing
// without the section.
func TestAnAcknowledgedAttachNamesItsSkewInTheBriefing(t *testing.T) {
	s := newSkewAttach(t, false, false, "", map[string]string{AllowAttachSkewEnv: "1"})
	if rc, restarted := s.attach(); rc != 0 || restarted || !s.didExec() {
		t.Fatalf("the acknowledgment must proceed: rc=%d restarted=%v\n%s", rc, restarted, s.stderr)
	}
	if s.o.attachSkewNotice != nil {
		t.Error("the attach left its skew on Options, where a later briefing could pick it up")
	}
	body := claudeBriefingOf(t, "yolo-ws-abcd1234")
	head := strings.Index(body, skewSectionHeading)
	if head < 0 {
		t.Fatalf("the acknowledged attach's briefing has no skew section:\n%s", body)
	}
	section := body[head:]
	if end := strings.Index(section[len(skewSectionHeading):], "\n## "); end >= 0 {
		section = section[:len(skewSectionHeading)+end]
	}
	for _, want := range []string{
		"**The jail was launched with**: yolo 0.10.0",
		"**It lacks the `agent-env-files` contract**",
		"claude (profile zai): ",
		"ZAI_API_KEY",
		"`yolo stop` from this workspace on the host, then a new launch",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the section does not say %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "tok-9") {
		t.Errorf("the section carries a credential VALUE:\n%s", section)
	}
	if !strings.Contains(s.stderr.String(), "The jail's briefing names this difference as well") {
		t.Errorf("the acknowledgment does not say the briefing carries it:\n%s", s.stderr)
	}

	// The next entry into the same jail selects nothing, so it needs nothing the jail lacks, and
	// its refresh of the same briefing carries no section.
	s.o.ProfileName = ""
	s.o.Getenv = func(string) string { return "" }
	s.channel = channelFor(t, s.o, s.cfg, s.packs, hydratedKey())
	if rc, restarted := s.attach(); rc != 0 || restarted {
		t.Fatalf("the plain re-entry failed: rc=%d restarted=%v\n%s", rc, restarted, s.stderr)
	}
	if body := claudeBriefingOf(t, "yolo-ws-abcd1234"); strings.Contains(body, skewSectionHeading) {
		t.Errorf("an entry that needs nothing the jail lacks kept the section:\n%s", body)
	}
}

// TestAnAcknowledgedAttachSaysWhenNoBriefingCarriesTheSkew: the stderr line for each jail whose
// briefing this attach cannot reach — packs it could not read (no refresh at all), packs that
// declare no briefing, and an Apple Container jail, which keeps the copy its launch made.
func TestAnAcknowledgedAttachSaysWhenNoBriefingCarriesTheSkew(t *testing.T) {
	for _, tc := range []struct {
		name string
		rt   string
		view attachPackView
		want string
	}{
		{"unreadable packs", "podman", attachPackView{unreadable: true}, "could not be read"},
		{"no briefing destination", "podman", attachPackView{staged: stagedPacks{packs: zaiOnly(t)}}, "declare no briefing"},
		{"Apple Container", "container", attachPackView{staged: stagedPacks{packs: zaiSelected(t)}}, "Apple Container jail keeps"},
		{"a briefing", "podman", attachPackView{staged: stagedPacks{packs: zaiSelected(t)}}, "briefing names this difference as well"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _, stderr := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(), nil)
			o.noteAttachSkewBriefing(tc.rt, tc.view)
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("want %q:\n%s", tc.want, stderr)
			}
		})
	}
}

// zaiOnly is a pack set that declares no briefing destination.
func zaiOnly(t *testing.T) []*packload.Pack {
	t.Helper()
	packs := []*packload.Pack{officialPack(t, "zai")}
	if len(briefingDestinations(packs)) != 0 {
		t.Fatal("zai declares a briefing now, so it no longer stands for a pack set with none")
	}
	return packs
}

// TestAnAcknowledgedAttachNamesAnUnstartedDaemonInTheBriefing: the third skew an attach can
// acknowledge, a profile-served daemon the running jail never started (OQ-CN7 (b)), reaches the
// briefing too.
func TestAnAcknowledgedAttachNamesAnUnstartedDaemonInTheBriefing(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "codex"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
	enable := func(o *Options, cfg *jsonx.OrderedMap) {
		o.UseProfiles = map[string]string{"codex": "bedrock"}
		v, _ := awsAuthServedConfig(t, packs).Get("loopholes")
		cfg.Set("loopholes", v)
		withBedrockRegion(cfg)
		o.Getenv = func(k string) string {
			if k == AllowAttachSkewEnv {
				return "1"
			}
			return ""
		}
	}
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(), enable)
	if rc, restarted, execed := attachToExec(t, o, cfg, packs, channel); rc != 0 || restarted || !execed {
		t.Fatalf("the acknowledgment must proceed: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, stderr)
	}
	body := claudeBriefingOf(t, "yolo-ws-abcd1234")
	for _, want := range []string{skewSectionHeading, `"aws-auth" jail daemon`, "an attach starts no daemon"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refreshed briefing does not say %q:\n%s", want, body)
		}
	}
}
