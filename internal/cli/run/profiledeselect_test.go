package run

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// deselectLaunchLine composes the real channel for packs under cfg (its `profile` key the
// launch's persistent selection) and the -p pairs useProfiles, and returns the launch's profile
// disclosure over it, as noteUseProfiles prints it.
func deselectLaunchLine(t *testing.T, packs []*packload.Pack, cfg *jsonx.OrderedMap,
	useProfiles map[string]string) string {
	t.Helper()
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.UseProfiles = useProfiles
	providers := jsonx.NewOrderedMap()
	bedrock := jsonx.NewOrderedMap()
	bedrock.Set("region", "eu-west-1")
	providers.Set("bedrock", bedrock)
	cfg.Set("providers", providers)
	channel := channelFor(t, o, cfg, packs, emptyEnv())
	out.Reset()
	o.noteUseProfiles(channel, packs, nil)
	return out.String()
}

// THE "REACHES NOTHING" WARNING NAMES WHERE THE SELECTION CAME FROM AND THE PERSISTENT FIX. A
// `profile` key in the user config reached copilot, which has no Bedrock client of its own, and
// the warning used to name only `-p copilot=<name>` — on every launch, since nothing the user
// typed made the selection. It now names the key's file and line and prints the key respelled
// with a null for copilot; that spelling, written back, parses, keeps claude on bedrock and
// removes the warning. Deleting the launch's Deselect (profilechannel.go, run.go) fails here.
func TestTheReachesNothingWarningNamesTheKeyAndItsFix(t *testing.T) {
	home := retireHome(t)
	writeUserConfigJSON(t, home, "{\n  \"packs\": [],\n  \"profile\": \"bedrock\"\n}\n")
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "copilot"),
		officialPack(t, "bedrock"), officialPack(t, "aws-auth")}

	got := deselectLaunchLine(t, packs, newConfig("profile", "bedrock"), nil)
	const warned = `Warning: profile "bedrock" reaches nothing for copilot`
	if !strings.Contains(got, warned) {
		t.Fatalf("the fixture no longer warns %q:\n%s", warned, got)
	}
	if want := "your config's `profile` key at ~/.config/yolo-jail/config.jsonc:3:14"; !strings.Contains(got, want) {
		t.Errorf("the warning does not name where the selection was written (%q):\n%s", want, got)
	}
	spelled := regexp.MustCompile("write `(\"profile\": [^`]*)` there").FindStringSubmatch(got)
	if spelled == nil {
		t.Fatalf("the warning prints no persistent spelling:\n%s", got)
	}
	if spelled[1] != `"profile": {"*": "bedrock", "copilot": null}` {
		t.Errorf("persistent spelling = %s", spelled[1])
	}
	doc, err := jsonx.Decode([]byte("{" + spelled[1] + "}"))
	if err != nil {
		t.Fatalf("the printed spelling %s does not parse: %v", spelled[1], err)
	}
	value, _ := doc.(*jsonx.OrderedMap).Get("profile")

	writeUserConfigJSON(t, home, "{\n  \"packs\": [],\n  "+spelled[1]+"\n}\n")
	fixed := deselectLaunchLine(t, packs, newConfig("profile", value), nil)
	if strings.Contains(fixed, "reaches nothing for copilot") {
		t.Errorf("the printed spelling, written back, still warns:\n%s", fixed)
	}
	if !strings.Contains(fixed, `claude → provider "bedrock"`) {
		t.Errorf("the printed spelling took the selection from claude too:\n%s", fixed)
	}
}

// A -p made the selection, so the warning names the -p pair that selects none, and no config
// spelling: nothing persistent wrote it.
func TestTheReachesNothingWarningNamesTheFlagWhenAFlagSelected(t *testing.T) {
	retireHome(t)
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "copilot"),
		officialPack(t, "bedrock"), officialPack(t, "aws-auth")}
	got := deselectLaunchLine(t, packs, newConfig(),
		map[string]string{"claude": "bedrock", "copilot": "bedrock"})
	if want := "the selection is this launch's `-p`, so add `-p copilot=` to it"; !strings.Contains(got, want) {
		t.Errorf("a -p selection's warning does not name its fix (%q):\n%s", want, got)
	}
	if strings.Contains(got, "`profile` key") {
		t.Errorf("a -p selection's warning points at the config key:\n%s", got)
	}
}
