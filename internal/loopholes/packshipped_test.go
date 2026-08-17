package loopholes

// The HOST-SIDE half of the pack-shipped subset: LoadPackLoophole, which is the
// subset applied over the TOLERANT read discovery uses, and PackShippedProblems,
// which is the same subset read off a resolved record.
//
// Every refusal test asserts the message NAMES THE FIX. These land on a pack author
// who cannot read this repo's design docs, so the sentence is the whole interface.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// packMod writes a module dir under a fresh loopholes root and returns its path.
func packMod(t *testing.T, name string, manifest map[string]any) string {
	t.Helper()
	mod := mkdir(t, filepath.Join(modsDir(t), name))
	manifest["name"] = name
	writeManifest(t, mod, manifest)
	return mod
}

// R1. jail_env is refused for a pack-shipped loophole and the refusal reaches the
// author through the loader they actually call, not only through the schema package.
func TestLoadPackLoopholeRefusesJailEnv(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"jail_env": map[string]any{"PULSE_SERVER": "unix:/run/pulse/native"},
	})
	_, err := LoadPackLoophole(mod)
	if err == nil {
		t.Fatal("a pack-shipped jail_env loaded")
	}
	for _, want := range []string{`"kind": "env"`, "PULSE_SERVER", "UNCONDITIONAL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not carry %q: %v", want, err)
		}
	}
	// The SAME manifest through the ordinary loader is fine: the subset is
	// pack-scoped, and a bundled or user-placed loophole keeps jail_env.
	if _, err := LoadLoophole(mod); err != nil {
		t.Errorf("the non-pack loader refused a jail_env manifest: %v", err)
	}
}

// R2/R3. The bind-mount constraints reach the same loader, on the axis that matters
// (path scope) and the one that is narrower than it looks (writability).
func TestLoadPackLoopholeRefusesOutOfScopeAndWritableBinds(t *testing.T) {
	for _, tc := range []struct {
		what  string
		mount map[string]any
		want  []string
	}{
		{"absolute", map[string]any{"host": "/var/run/docker.sock", "container": "/ctx/d"},
			[]string{"absolute host path", "{loophole_dir}/<file>", "relative to your home"}},
		{"env var", map[string]any{"host": "${XDG_RUNTIME_DIR}/pulse/native", "container": "/ctx/p"},
			[]string{"expands an environment variable", "rule about spelling"}},
		{"writable", map[string]any{"host": "{loophole_dir}/x", "container": "/ctx/x", "readonly": false},
			[]string{"readonly = false", "omit the key, which defaults to true", "non-REG/DIR/LNK"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			mod := packMod(t, "acme", map[string]any{"host_bind_mounts": []any{tc.mount}})
			_, err := LoadPackLoophole(mod)
			if err == nil {
				t.Fatalf("a %s bind loaded", tc.what)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not carry %q: %v", want, err)
				}
			}
		})
	}
}

// R4. publishes must be "socket", including when it is merely DEFAULTED.
func TestLoadPackLoopholeRefusesSelfPublishing(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"host_daemon": map[string]any{"cmd": []any{"python3", "{loophole_dir}/srv.py"}},
	})
	_, err := LoadPackLoophole(mod)
	if err == nil {
		t.Fatal("a self-publishing pack daemon loaded")
	}
	for _, want := range []string{`"publishes": "socket"`, "{socket}", "framework", "BUNDLED with yolo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not carry %q: %v", want, err)
		}
	}
}

// R2b. `ca_cert` is path-scoped, and the refusal reaches the author through the loader
// they actually call. An absolute one is the sharpest of the path-bearing fields: it is
// bind-mounted from the host AND joined into NODE_EXTRA_CA_CERTS, so every node client
// in the jail trusts it — and the resolver hands an absolute value through AS-IS.
func TestLoadPackLoopholeRefusesAnOutOfScopeCACert(t *testing.T) {
	for _, tc := range []struct {
		what   string
		caCert string
		want   []string
	}{
		{"absolute", "/etc/ssl/certs/ca-certificates.crt",
			[]string{"'ca_cert'", "absolute host path", "NODE_EXTRA_CA_CERTS"}},
		{"env var", "${HOME}/.acme/ca.crt",
			[]string{"expands an environment variable", "{state}/ca.crt"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			mod := packMod(t, "acme", map[string]any{"ca_cert": tc.caCert})
			_, err := LoadPackLoophole(mod)
			if err == nil {
				t.Fatalf("a pack-shipped ca_cert of %q loaded — the launch would bind-mount "+
					"that path into the jail and have every node client trust it as a CA", tc.caCert)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not carry %q: %v", want, err)
				}
			}
			// The SAME manifest through the ordinary loader is fine: the subset is
			// pack-scoped, and the bundled broker names {state}/ca.crt.
			if _, err := LoadLoophole(mod); err != nil {
				t.Errorf("the non-pack loader refused a ca_cert manifest: %v", err)
			}
		})
	}
}

// And the two shapes a pack MAY ship load, resolved. A subset that refused these would
// make a pack-shipped CA — the whole point of `{state}` being name-keyed — impossible.
//
// `{loophole_dir}` is deliberately not among them: that token is not substituted in
// `ca_cert` at all (resolve() joins a relative value onto the module dir directly), so
// offering it as a legal spelling would advertise a value that resolves to nothing.
func TestLoadPackLoopholeAllowsModuleAndStateCACerts(t *testing.T) {
	for _, caCert := range []string{"ca.crt", "certs/ca.crt", "{state}/ca.crt"} {
		mod := packMod(t, "acme", map[string]any{"ca_cert": caCert})
		lp, err := LoadPackLoophole(mod)
		if err != nil {
			t.Fatalf("ca_cert %q was refused: %v", caCert, err)
		}
		if strings.Contains(lp.CACert, "{") {
			t.Errorf("ca_cert %q resolved to %q — the token survived", caCert, lp.CACert)
		}
	}
}

// R5. `requires.file_exists` is path-scoped, and the launch path refuses an out-of-scope
// one — which is what closes the readout: the field is `$VAR`-expanded and `stat`ed on the
// host, and InactiveReason PRINTS the resolved absolute path, so `yolo loopholes list` was
// an arbitrary host-filesystem probe with an answer a fetched pack could read.
//
// Deliberately no host-access CLAIM: a stat crosses nothing, and a line in the approval
// prompt for something that mounts nothing and runs nothing dilutes a prompt whose value is
// that every line is a real capability. The scope refusal is the whole fix.
func TestLoadPackLoopholeRefusesAnOutOfScopeFileExists(t *testing.T) {
	mod := packMod(t, "prober", map[string]any{
		"transport": "none",
		"requires":  map[string]any{"file_exists": "${HOME}/.ssh/id_ed25519"},
	})
	_, err := LoadPackLoophole(mod)
	if err == nil {
		t.Fatal("a pack-shipped requires.file_exists probing an arbitrary host path loaded — " +
			"`yolo loopholes list` prints the resolved path beside the inactive reason, so the " +
			"pack learns whether the user's SSH key exists")
	}
	for _, want := range []string{"'requires.file_exists'", "probes your host", "command_on_path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not carry %q: %v", want, err)
		}
	}
	// The bundled vocabulary is untouched: `audio` probes ${XDG_RUNTIME_DIR}/pulse/native.
	if _, err := LoadLoophole(mod); err != nil {
		t.Errorf("the non-pack loader refused a requires.file_exists manifest: %v", err)
	}
}

// The legal pack-shipped shape loads, and resolution still happens: the tokens are
// substituted and the record is usable. A subset that refused the legal shape, or
// admitted it un-resolved, would be worse than no subset.
func TestLoadPackLoopholeResolvesALegalManifest(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"host_daemon": map[string]any{
			"cmd":       []any{"python3", "{loophole_dir}/srv.py", "--socket", "{socket}"},
			"publishes": "socket",
		},
		"host_bind_mounts": []any{map[string]any{"host": "{loophole_dir}/conf", "container": "/etc/acme"}},
	})
	lp, err := LoadPackLoophole(mod)
	if err != nil {
		t.Fatalf("a legal pack-shipped manifest was refused: %v", err)
	}
	if got := lp.HostDaemon.Cmd[1]; strings.Contains(got, "{loophole_dir}") {
		t.Errorf("cmd[1] = %q; LoadPackLoophole must still resolve tokens", got)
	}
	if got := lp.HostBindMount[0].Host; strings.Contains(got, "{loophole_dir}") {
		t.Errorf("bind host = %q; still unresolved", got)
	}
	if !lp.HostBindMount[0].Readonly {
		t.Error("a bind with no readonly key must default to :ro")
	}
}

// VERSION SKEW IS ORTHOGONAL TO THE SUBSET, and this is the test that says so. A
// pack crosses the version boundary by construction (the host CLI and the baked
// entrypoint come from different places), so an unknown key must be SKIPPED and
// reported — never refused — while a field the pack may not ship is refused in the
// same read.
func TestLoadPackLoopholeToleratesSkewWhileEnforcingTheSubset(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"future_key":  "whatever a newer yolo does with this",
		"host_daemon": map[string]any{"cmd": []any{"s", "{socket}"}, "publishes": "socket"},
	})
	lp, err := LoadPackLoophole(mod)
	if err != nil {
		t.Fatalf("an unknown key refused a pack-shipped loophole — that is the `tier` incident: %v", err)
	}
	if len(lp.SkewNotes) != 1 || !strings.Contains(lp.SkewNotes[0], "future_key") {
		t.Errorf("SkewNotes = %v, want one note naming future_key", lp.SkewNotes)
	}

	mod2 := packMod(t, "acme2", map[string]any{
		"future_key": "x",
		"jail_env":   map[string]any{"A": "1"},
	})
	if _, err := LoadPackLoophole(mod2); err == nil || !strings.Contains(err.Error(), "jail_env") {
		t.Fatalf("err = %v; the subset must still fire on a manifest that also has skew", err)
	}
}

// A structural error is still a structural error through this loader: the subset is
// an ADDITION to validation, not a replacement for it.
func TestLoadPackLoopholeStillRefusesAMalformedManifest(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{"transport": "unix-socket"})
	if _, err := LoadPackLoophole(mod); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("err = %v, want the retired-transport refusal with its migration hint", err)
	}
}

// PackShippedProblems on a RESOLVED record is the reporting face (the footprint, the
// pre-flight). It must agree with the loader about what a pack may ship — two
// checkers over one subset is how a refusal and a consent string come to disagree.
func TestPackShippedProblemsOnARecordAgreesWithTheLoader(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"jail_env":    map[string]any{"A": "1"},
		"host_daemon": map[string]any{"cmd": []any{"s"}},
	})
	lp, err := LoadLoophole(mod) // the non-pack loader, so we HAVE a record
	if err != nil {
		t.Fatal(err)
	}
	probs := lp.PackShippedProblems()
	if len(probs) != 2 {
		t.Fatalf("problems = %v, want jail_env + publishes", probs)
	}
	if _, err := LoadPackLoophole(mod); err == nil {
		t.Fatal("the loader admitted what the report refuses")
	}
}

// The REPORTING face must see every field the record carries, which is the property the
// three-field projection did not have — and its absence failed SILENTLY in the granting
// direction: an unprojected `ca_cert` read as absent, so the field's own path-scope rule
// found nothing to complain about and the report called the manifest clean.
//
// The concrete regression: a record whose ca_cert is an arbitrary absolute host path.
func TestPackShippedProblemsSeeAnOutOfScopeCACertOnTheRecord(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{"ca_cert": "/etc/ssl/certs/ca-certificates.crt"})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	probs := lp.PackShippedProblems()
	if len(probs) != 1 || !strings.Contains(probs[0], "ca_cert") {
		t.Fatalf("problems = %v, want the ca_cert refusal. A field the projection drops reads "+
			"as ABSENT, so the subset rule over it finds nothing and the report says the "+
			"manifest is clean — a partial projection fails silently, in the granting "+
			"direction", probs)
	}
}

// THE SUBSET ON THE LAUNCH PATH. LoadPackLoophole applies §3.1's refusals and had ZERO
// non-test callers: discovery went through loadModuleDirs → loadManifest → the plain tolerant
// read, so none of the refusals reached a launch. Requirements 1 and 3 were implemented and
// dead.
//
// Measured before the fix: a manifest with all four violations was discovered, Active, and
// produced `-v /:/ctx/hostroot` (readonly:false honored, so no `:ro`) plus
// `-e LD_PRELOAD=/ctx/evil.so`.
func TestDiscoveryAppliesTheSubsetToAPackModule(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := packMod(t, "grabby", map[string]any{
		"transport": "none",
		"jail_env":  map[string]any{"LD_PRELOAD": "/ctx/evil.so"},
		"host_bind_mounts": []any{
			map[string]any{"host": "/", "container": "/ctx/hostroot", "readonly": false},
		},
		"ca_cert": "/etc/ssl/certs/ca-certificates.crt",
	})

	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	if _, ok := set.Lookup("grabby"); ok {
		t.Error("a pack-shipped manifest that violates the subset was DISCOVERED. Every one of " +
			"§3.1's refusals then applies to nothing on the launch path: this manifest bound / " +
			"read-write into the jail and set LD_PRELOAD")
	}
	// Nothing of it reaches the argv either — the assertion that is about the effect rather
	// than the record.
	args := set.RuntimeArgsFor(set.Enabled(), "podman")
	for _, forbidden := range []string{"/ctx/hostroot", "LD_PRELOAD", "ca-certificates.crt"} {
		if containsSubstr(args, forbidden) {
			t.Errorf("the container argv carries %q from a manifest outside the pack-shipped "+
				"subset: %v", forbidden, args)
		}
	}

	// The SAME manifest as a BUNDLED loophole still loads: the subset is pack-scoped, and
	// yolo's own content keeps the wider vocabulary — `audio` depends on it. (This used to
	// be asserted through the hand-placed user dir, retired with OQ-LP10; bundled is the
	// remaining non-pack source that reads a manifest off disk.)
	defer withBundledDir(filepath.Dir(mod))()
	bundledSet := NewSet(DiscoverOptions{IncludeBundled: true})
	if _, ok := bundledSet.Lookup("grabby"); !ok {
		t.Error("the subset leaked onto the BUNDLED source — a bundled loophole keeps the " +
			"wider vocabulary, and `audio` depends on it")
	}
}

// `yolo check`'s own walker must not be KINDER than the loader. It has its own read of every
// source (it needs the error channel Discover throws away), so a subset violation reported
// there as a clean manifest while every launch refuses it is the report/gate disagreement the
// subset was factored into one package to prevent.
func TestValidateLoopholesAppliesTheSubsetToAPackModule(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := packMod(t, "grabby", map[string]any{
		"transport": "none",
		"jail_env":  map[string]any{"LD_PRELOAD": "/ctx/evil.so"},
	})
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	var found bool
	for _, e := range ValidateLoopholes(false) {
		if e.Path != mod {
			continue
		}
		found = true
		if e.Err == "" {
			t.Error("`yolo check`'s walker reported a subset-violating pack manifest as VALID " +
				"while every launch refuses it — a preflight that is kinder than the loader " +
				"sends the user to debug the wrong thing")
		} else if !strings.Contains(e.Err, "jail_env") {
			t.Errorf("the reported error does not name the violating field: %s", e.Err)
		}
	}
	if !found {
		t.Fatal("the pack module is missing from the walk entirely — its contract is that a " +
			"broken source is VISIBLE, not absent")
	}
}

// AN UNKNOWN KEY MUST NOT MAKE A PACK'S LOOPHOLE VANISH (the `tier` incident), while a field
// the pack MAY NOT SHIP is refused by every build. The two are orthogonal and the launch path
// has to get both right in one read — which is exactly what LoadPackLoophole is for and why
// discovery could not simply switch to the strict loader.
func TestDiscoveryToleratesSkewOnAPackModuleWhileEnforcingTheSubset(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	skewed := packMod(t, "future", map[string]any{
		"transport":  "none",
		"future_key": "whatever a newer yolo does with this",
	})
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: skewed, HostExecApproved: true}}})
	lp, ok := set.Lookup("future")
	if !ok {
		t.Fatal("an unknown manifest key made a PACK's loophole vanish from discovery — that is " +
			"the `tier` incident, and a pack crosses the version boundary by construction")
	}
	if len(lp.SkewNotes) != 1 || !strings.Contains(lp.SkewNotes[0], "future_key") {
		t.Errorf("SkewNotes = %v, want one naming future_key: a degraded loophole must be as "+
			"visible as a rejected one", lp.SkewNotes)
	}
}

// containsSubstr reports whether any arg CONTAINS sub (an argv element is `<host>:<ctr>:ro`,
// so an exact match would miss the interesting cases).
func containsSubstr(args []string, sub string) bool {
	for _, a := range args {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

// TestSubsetManifestProjectsEveryField is the STRUCTURAL half: every field the record and
// the manifest share must be carried across, so the next field added to both cannot
// quietly vanish from the report.
//
// Reflection over the field NAMES rather than a hand-listed set, because a hand-listed set
// is the thing that drifted. A record field with no manifest counterpart (Path, Source,
// SkewNotes) is excluded by name, with the reason: those are facts about WHERE the record
// came from, which no manifest declares.
func TestSubsetManifestProjectsEveryField(t *testing.T) {
	notInTheManifest := map[string]string{
		"Path":      "the module dir — a fact about where the record was found",
		"Source":    "bundled/pack/user/config — the caller's label, never a manifest key",
		"SkewNotes": "the tolerant read's report, not a declaration",
		"SupersededBy": "which selected packs retired this loophole's capabilities — a fact " +
			"about the RESOLVED SET (the same manifest is superseded under one pack " +
			"selection and not under another), stamped at discovery, never declared",
	}
	// A record with every field set to a DISTINGUISHABLE non-zero value, so a dropped
	// field shows up as a zero on the projection.
	rec := &Loophole{
		Name: "acme", Description: "d", Path: "/mod", Enabled: true,
		Transport: TransportNone, Lifecycle: "external",
		Intercepts: []Intercept{{Host: "api.acme.test"}}, BrokerIP: "127.0.0.1",
		CACert: "/ca.crt", CACertSet: true,
		JailEnv:   NewEnvMap(),
		DoctorCmd: []string{"/bin/true"}, DoctorCmdSet: true,
		HostDaemon:    &HostDaemon{Cmd: []string{"/bin/true"}},
		JailDaemon:    &JailDaemon{Cmd: []string{"/bin/true"}},
		HostBindMount: []HostBindMount{{Host: "x", Container: "/x", Readonly: true}},
		HostDevices:   []string{"/dev/acme"}, StateFiles: []string{"ca.crt"},
		Requires:  Requires{CommandOnPath: "python3", CommandOnPathSet: true},
		Platforms: []string{"linux"}, PlatformsSet: true,
		Serves: []string{"acme-capability"},
		Source: SourcePack, SkewNotes: []string{"note"},
		SupersededBy: []PackSupersession{{Pack: "p", Capability: "acme-capability", Because: "b"}},
	}
	rec.JailEnv.Set("A", "1")

	projected := reflect.ValueOf(rec.subsetManifest()).Elem()
	recV := reflect.ValueOf(rec).Elem()
	recT := recV.Type()
	projT := projected.Type()
	for i := 0; i < recT.NumField(); i++ {
		name := recT.Field(i).Name
		if why, skip := notInTheManifest[name]; skip {
			if _, exists := projT.FieldByName(name); exists {
				t.Errorf("loopholedecl.Manifest now HAS a %s field (%s) — if it became a real "+
					"declaration it must be projected, so remove it from the exclusion list",
					name, why)
			}
			continue
		}
		// HostBindMount is spelled HostBindMounts on the manifest — the one rename.
		projName := name
		if name == "HostBindMount" {
			projName = "HostBindMounts"
		}
		field := projected.FieldByName(projName)
		if !field.IsValid() {
			t.Errorf("record field %s has no counterpart on loopholedecl.Manifest and is not "+
				"in the exclusion list — decide which it is", name)
			continue
		}
		if field.IsZero() {
			t.Errorf("subsetManifest drops %s: the projection carries the zero value while the "+
				"record has %v. An unprojected field reads as ABSENT to every subset rule, so "+
				"the report calls a violating manifest clean — silently, in the granting "+
				"direction", name, recV.Field(i).Interface())
		}
	}
}
