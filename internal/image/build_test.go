package image

import (
	"slices"
	"strings"
	"testing"
)

// TestOCIPreflightBuildNamesTheRequestedAttrs: the preflight builds what its caller
// says a launch on this host will realize, plus the copier it never has to ask for.
//
// MUTATION: hardcode ImageAttrDefault in ociPreflightBuild — which is what
// BuildOCIImage did, unconditionally — and the lean case fails.
func TestOCIPreflightBuildNamesTheRequestedAttrs(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  OCIBuildRequest
		want []string
	}{
		{"default", OCIBuildRequest{}, []string{ImageAttrDefault, ImageCopierAttr}},
		{"lean pair",
			OCIBuildRequest{Attr: ImageAttrLean, AlsoBuild: []string{ImageExtrasAttr}},
			[]string{ImageAttrLean, ImageCopierAttr, ImageExtrasAttr}},
	} {
		argv, _ := ociPreflightBuild(tc.req, "/tmp/link", nil)
		for _, want := range tc.want {
			if !slices.Contains(argv, want) {
				t.Errorf("%s: %s is not in the argv: %v", tc.name, want, argv)
			}
		}
		// The IMAGE attr must come first: the returned store path is the FIRST
		// out-link, and nix names the rest `<link>-1`, `<link>-2`.
		attr := tc.req.Attr
		if attr == "" {
			attr = ImageAttrDefault
		}
		first := -1
		for i, a := range argv {
			if strings.HasPrefix(a, ".#") {
				first = i
				break
			}
		}
		if first < 0 || argv[first] != attr {
			t.Errorf("%s: the first attr on the command line is not the image (%q): %v",
				tc.name, attr, argv)
		}
		if tc.req.Attr != ImageAttrLean && slices.Contains(argv, ImageAttrLean) {
			t.Errorf("%s: built the lean image nobody asked for: %v", tc.name, argv)
		}
	}
}

// TestOCIPreflightBuildExtraPackagesEnv covers the three environments a preflight can
// need, and the middle one is the one that did not exist: the lean image reads
// YOLO_EXTRA_PACKAGES the same way the default one does (flake.nix keeps
// `extraPackages` in the join for every non-minimal variant), so an ambient value in
// the calling shell would bake packages into the image C5 built lean.
//
// The third case is why the suppression is a FIELD and not "no packages were passed":
// a baked launch inherits that ambient variable too, so a preflight that scrubbed it
// there would prove a different image than the launch builds.
func TestOCIPreflightBuildExtraPackagesEnv(t *testing.T) {
	ambient := []string{"PATH=/bin", extraPackagesEnv + `=["ambient"]`}

	_, env := ociPreflightBuild(OCIBuildRequest{ExtraPackages: []any{"zbar"}}, "/l", ambient)
	if got := envValue(env, extraPackagesEnv); got != `["zbar"]` {
		t.Errorf("declared packages: %s=%q, want the config's list", extraPackagesEnv, got)
	}

	_, env = ociPreflightBuild(OCIBuildRequest{
		Attr:               ImageAttrLean,
		NoExtraPackagesEnv: true,
	}, "/l", ambient)
	if _, present := lookupEnv(env, extraPackagesEnv); present {
		t.Errorf("the lean build carried %s: %v", extraPackagesEnv, env)
	}
	if !slices.Contains(env, "PATH=/bin") {
		t.Error("the scrub took the rest of the environment with it")
	}

	_, env = ociPreflightBuild(OCIBuildRequest{}, "/l", ambient)
	if got := envValue(env, extraPackagesEnv); got != `["ambient"]` {
		t.Errorf("a baked preflight must inherit the ambient %s exactly as the launch "+
			"does; got %q", extraPackagesEnv, got)
	}
	// The caller's environ is never mutated: os.Environ() is fresh, a fixture is not.
	if got := envValue(ambient, extraPackagesEnv); got != `["ambient"]` || len(ambient) != 2 {
		t.Errorf("the caller's environ was modified: %v", ambient)
	}
}

// TestWithoutEnvVarDropsEveryOccurrence: a duplicated variable is legal in an environ
// block and the LAST one wins, so removing one occurrence can leave the value live.
func TestWithoutEnvVarDropsEveryOccurrence(t *testing.T) {
	got := withoutEnvVar([]string{"A=1", "X=first", "B=2", "X=last"}, "X")
	if slices.ContainsFunc(got, func(e string) bool { return strings.HasPrefix(e, "X=") }) {
		t.Errorf("X survived: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("collateral damage: %v", got)
	}
}

func lookupEnv(env []string, name string) (string, bool) {
	val, found := "", false
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			// Last occurrence wins, exactly as a process reads it.
			val, found = v, true
		}
	}
	return val, found
}

func envValue(env []string, name string) string {
	v, _ := lookupEnv(env, name)
	return v
}
