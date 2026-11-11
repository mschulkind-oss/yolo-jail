package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Drive the full diagnostic caller: removing the Check call must lose the warning.
// PATH contains no host programs, so neither AWS nor a pack doctor can run.
func TestCheckWarnsForAWSS3WithUnnarrowedAWSAuth(t *testing.T) {
	for _, tc := range []struct {
		name, packs, settings string
		warn                  bool
	}{
		{"paired un-narrowed", `"aws-s3", "aws-auth"`, "unnarrowed = true", true},
		{"paired narrowed role", `"aws-s3", "aws-auth"`, `role_arn = "arn:aws:iam::123456789012:role/limited"`, false},
		{"paired narrowed session policy", `"aws-s3", "aws-auth"`, "role_arn = \"arn:aws:iam::123456789012:role/limited\"\nsession_policy = '{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"bedrock:InvokeModel\",\"Resource\":\"*\"}]}'", false},
		{"paired explicit false", `"aws-s3", "aws-auth"`, "unnarrowed = false", false},
		{"paired unspecified mode", `"aws-s3", "aws-auth"`, "", false},
		{"auth absent", `"aws-s3"`, "unnarrowed = true", false},
		{"s3 absent", `"aws-auth"`, "unnarrowed = true", false},
		{"both absent", "", "unnarrowed = true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			opts := warnChannelFixture(t, &out, `{}`)
			t.Setenv("PATH", t.TempDir())
			t.Setenv("YOLO_USER_LAYER", "")
			userDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
			body := "packs = [" + tc.packs + "]\n"
			if strings.Contains(tc.packs, `"aws-auth"`) {
				body += "[allow.aws-auth]\nprofile = \"fixture\"\n" + tc.settings + "\n"
			}
			writeCheckFile(t, filepath.Join(userDir, "config.toml"), body, 0o600)
			if exit := Check(opts); exit != 0 {
				t.Fatalf("fixture check exit = %d, want warning-only success:\n%s", exit, out.String())
			}
			got := stripANSI(out.String())
			const title = "aws-s3 with un-narrowed aws-auth: S3 mediation can be bypassed"
			if has := strings.Contains(got, "[WARN] "+title); has != tc.warn {
				t.Fatalf("warning present = %v, want %v:\n%s", has, tc.warn, got)
			}
			if !strings.Contains(got, "Packs") {
				t.Fatalf("Check never reached selected packs:\n%s", got)
			}
			if strings.Contains(got, "[FAIL] "+title) {
				t.Fatalf("bypass diagnostic must not refuse:\n%s", got)
			}
			if tc.warn {
				for _, want := range []string{"directly delivered credentials", "SDK", "absolute path", "if that profile permits S3", "yolo config why allow.aws-auth.unnarrowed", "Keep this auth route if intended", "does not inspect IAM permissions"} {
					if !strings.Contains(got, want) {
						t.Errorf("warning lacks %q:\n%s", want, got)
					}
				}
				var configOut bytes.Buffer
				opts.Stdout = &configOut
				if exit := ConfigCheck(opts); exit != 0 {
					t.Fatalf("config-only check exit = %d:\n%s", exit, configOut.String())
				}
				if strings.Contains(configOut.String(), title) {
					t.Fatalf("config-only check emitted the full-check diagnostic:\n%s", configOut.String())
				}
			}
		})
	}
}
