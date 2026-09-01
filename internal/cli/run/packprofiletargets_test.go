package run

import (
	"bytes"
	"strings"
	"testing"
)

// This file pins the FLAG half of the CLI-name namespace
// (profiles-as-pack-variants.md §2.5, §3.3): `--pack-profile <cli>=<name>` and
// `-p <name> -- <bin>` key a profile by CLI name, and a name no resolvable pack
// installs is refused at launch. The CONFIG half is validated by ValidateConfig;
// the flags never reach a config validator, so the launch pipeline owns this
// check — the same silent-typo hole §2.5 documents, arriving through argv.
//
// Driven through stageRunPacks (the launch path, above the backend dispatch and
// covering attach too), not the checker, so a test fails if the check is unwired
// from staging.

// A --pack-profile naming a CLI no pack installs is fatal, naming the CLI and the
// installed names.
func TestStageRunPacksRefusesAnUnknownPackProfileCLI(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.PackProfiles = map[string]string{"cloude": "bedrock"}
	if _, ok := o.stageRunPacks("yolo-profile-target-cli"); ok {
		t.Fatalf("a --pack-profile naming a CLI no pack installs staged cleanly — " +
			"the typo passes silently")
	}
	if !strings.Contains(out.String(), `no pack installs a CLI named "cloude"`) {
		t.Errorf("the refusal must name the unknown CLI:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "claude") {
		t.Errorf("the refusal must list the installed CLI names, including claude:\n%s",
			out.String())
	}
}

// -p with a command keys the profile by the command's binary name; an unknown one
// is the same refusal.
func TestStageRunPacksRefusesAProfileNameKeyedToAnUnknownCommand(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.ProfileName = "dev"
	o.Args = []string{"cloude"}
	if _, ok := o.stageRunPacks("yolo-profile-target-bin"); ok {
		t.Fatalf("-p keying a profile to a command no pack installs staged cleanly")
	}
	if !strings.Contains(out.String(), `no pack installs a CLI named "cloude"`) {
		t.Errorf("the refusal must name the unknown command binary:\n%s", out.String())
	}
}

// The positive direction: selectors naming CLIs the embedded packs install stage
// cleanly. Without this, the check could refuse everything and the two tests above
// would still pass.
func TestStageRunPacksAcceptsProfileTargetsThePacksInstall(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.ProfileName = "dev"
	o.Args = []string{"claude"}
	o.PackProfiles = map[string]string{"pi": "glm"}
	if _, ok := o.stageRunPacks("yolo-profile-target-known"); !ok {
		t.Fatalf("selectors naming installed CLIs must stage cleanly:\n%s", out.String())
	}
}
