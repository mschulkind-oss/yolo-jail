package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// reportAWSS3CredentialBypass is a full-check diagnostic only. Pack selection and
// the explicit un-narrowed switch show a possible bypass, not the profile's IAM
// permissions; this must never read a credential or change the chosen auth route.
func (o *Options) reportAWSS3CredentialBypass(r *reporter, merged *jsonx.OrderedMap) {
	var s3, auth bool
	for _, pack := range o.selectedPacks {
		s3 = s3 || pack.Name == "aws-s3"
		auth = auth || pack.Name == "aws-auth"
	}
	if !s3 || !auth {
		return
	}
	settings := loopholes.SettingsFromConfig(loopholeConfigBlock(merged), "aws-auth")
	if settings == nil {
		return
	}
	// aws-auth serves nothing without a profile, so there is no bypass to report.
	profileValue, _ := settings.Get("profile")
	profile, _ := profileValue.(string)
	if strings.TrimSpace(profile) == "" {
		return
	}
	value, _ := settings.Get("unnarrowed")
	unnarrowed, _ := value.(bool)
	if !unnarrowed {
		return
	}
	// The inspect verb takes the key where the user's config keeps it: `[allow.aws-auth]` under TOML,
	// `loopholes.aws-auth.settings` under a legacy JSONC file.
	inspectKey := "loopholes.aws-auth.settings.unnarrowed"
	if loopholes.SelectionIsTheSwitch() {
		inspectKey = "allow.aws-auth.unnarrowed"
	}
	r.warn("aws-s3 with un-narrowed aws-auth: S3 mediation can be bypassed",
		"aws-s3 governs the wrapped aws CLI only. aws-auth's directly delivered credentials let an SDK or an aws binary reached by absolute path bypass that mediation if that profile permits S3.\n"+
			"Run `yolo config why "+inspectKey+"` on the host to review the selected mode. If broker-only S3 access is wanted, ask your AWS administrator for a profile or role without S3 permissions. Keep this auth route if intended; this warning does not inspect IAM permissions, and a narrowed role is not proof that S3 access is absent.")
}
