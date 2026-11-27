package awsclassify

import (
	"strings"
	"testing"
)

// F5: routing changes nothing the jail may say. The classifier never sees a route or a
// context (its input has no field for one), and a profile, region or endpoint in the jail's
// argv is refused as a credential option exactly as at baseline, for a bucket a route would
// cover and for one it would not, without echoing the value.
func TestDeclaredExecutionContexts(t *testing.T) {
	t.Run("F5", func(t *testing.T) {
		for _, root := range []string{"s3://bkt/team/", "s3://bkt", "s3://"} {
			for _, argv := range [][]string{
				{"s3", "ls", "s3://bkt/team/", "--profile", "jail-chosen"},
				{"--profile=jail-chosen", "s3", "ls", "s3://bkt/team/"},
				{"s3", "cp", "s3://bkt/team/k", "-", "--region", "eu-west-1"},
				{"s3", "ls", "s3://bkt/team/", "--endpoint-url", "http://jail-chosen"},
			} {
				a := Classify(input([]string{root}, argv...))
				if a.Refused == nil || a.Refused.Class != "credential" || a.Classification != nil {
					t.Fatalf("root %s argv %v: not refused as credential: %+v", root, argv, a)
				}
				if strings.Contains(a.Refused.Reason, "jail-chosen") {
					t.Fatalf("a jail-chosen value was echoed: %q", a.Refused.Reason)
				}
			}
		}
	})
}
