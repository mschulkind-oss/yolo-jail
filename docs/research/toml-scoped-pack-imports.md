---
title: "Direct TOML pack scoping needs a contribution projection"
status: accepted
stage: CURRENT
planning-actions: none
next: "Use the companion design and plan for direct user-level imports"
tags: [packs, skills, toml, research]
---

# Direct TOML pack scoping needs a contribution projection

**Evidence:** re-analyzed from source, 2026-11-17, at `a4dfc67d847d7d7c0b5fd06515cf93e86a06ad54`.
This is a baseline survey, **not evidence that scoped imports are implemented**.
Yolo manages the coding environment per workspace; this feature concerns which user-selected
skills reach that environment, not new confinement or credential authority.

**Reads with:** [the behavioral design](../design/toml-scoped-pack-imports.md) and
[the build hand-off](../design/toml-scoped-pack-imports-plan.md).

## Present grammar and naming

The [readable schema](../../internal/config/schema.go#L160-L175) permits user-only
`packs` entry tables with `git`, `ref`, `pack`, `bare` and `namespace`. Neither `subdir`
nor `skills` is present. [ParsePackReference](../../internal/packsrc/naming.go) separately
rejects unknown entry keys. Both boundaries must change to admit modern scoping.

- Bare words select known shipped packs. `yolo/<name>` preserves embedded identity.
- `local/<name>` names a directory under the user's config `packs/`; this is **not** the
  automatically appended conventional `local` content pack.
- Named-source handles have two components. A third component is refused.
- A direct Git table names the repository root and derives an owner/repository handle.
- Named local sources can already anchor at a nested filesystem path. Named Git sources
  have no independent root-directory field.
- `bare` preserves upstream names; otherwise a named source's key or an own-source word
  supplies the namespace. `namespace` does not override a named source's key.
- Omitted Git refs are supported in modern entries through `AllowDefaultRef`; legacy
  entries retain the explicit-ref requirement.

These facts are enforced by [the naming parser](../../internal/packsrc/naming.go),
[source-definition parser](../../internal/packsrc/references.go), and
[readable-entry lowering](../../internal/config/readablepacks.go).
The latter dispatches each legacy-origin item to the old parser and currently refuses
duplicate handles and physical local directories.

## Legacy filters are not a skill projection

[The legacy pack reader](../../internal/config/packs.go) accepts `source`, `name`, `only`
and `exclude`. [Pack staging](../../internal/packstage/packstage.go) filters file paths
before load: inclusion, then exclusion; a directory-prefix match keeps descendants.
Keeping a manifest keeps its declarations. Dropping it allows ordinary conventions again.
File filtering therefore cannot guarantee removal of programs, hooks, dependencies or plugins.

[ResolvePack](../../internal/config/packresolve.go) also sets `SourcedSkillsWithheld`
when legacy `Only` is nonempty. [Sourced-row resolution](../../internal/packload/sourcedskills.go)
skips those rows before store work. The new TOML producer must not populate these legacy wire
fields. Old-reader compatibility is required; adding new JSONC fields is not.

[Migration](../../internal/configmigrate/packs.go) refuses legacy objects carrying fields
other than `source`, retaining unconvertible files. Typed name selection cannot be translated
from old path filters without source-dependent proof. **Verdict:** retain refusal and give a
manual modern rewrite, not an automatic widening conversion.

## Whole raw repositories are not portable collections

[Pack loading](../../internal/packload/packload.go) accepts a directory without a manifest.
[Content governance](../../internal/packload/governance.go) still discovers conventional
skills and briefing contributions; pack loading separately discovers home and overlay content.

[collectSkills](../../internal/hostskills/deliver.go#L695-L738) enumerates immediate child
directories of governed source trees, later sources winning a name. It follows directory
links with `Stat`, ignores loose files, and does **not** require a marker. A tree
`skills/core-tools/skill-a/SKILL.md` consequently contributes `core-tools`, not a portable
registration named `skill-a`. Root markers and root collections have no conventional skills source.

[Plugin discovery](../../internal/pluginpack/pluginpack.go#L722-L781) recognizes a plugin
at the pack root or in an immediate child of a resolved skills source. Recognized manifest
locations are `.plugin`, the directory root, `.github/plugin`, and `.claude-plugin`.
[Pack plugin delivery](../../internal/packload/plugins.go) returns those plugins whole;
it is disclosure, not a skills-only gate. A manifestless pack is not an inert data carrier.

**Verdict:** wrapper-only manifest rows do not satisfy direct user-level imports. Raw collection
mode and typed pack mode need one explicit effective projection before closure and delivery.

## Reusable payload and registration boundaries

[Manifest sourced rows](../../internal/packdecl/sourcedskills.go) already name one non-root
Git skill with an explicit revision, alias, license and audience. They neither enumerate a
collection nor admit a root marker.

[ResolveSelectedSkillPayloads](../../internal/packload/sourcedskills.go) makes one grouped
store call over the selected closure. [The payload store](../../internal/packsrc/skillpayload.go)
groups requests by repository/ref and publishes by repository/commit, outside discovery roots.
It validates selected ancestry, modes, links and bounds; unsafe selected data refuses, unsafe
supporting data can be omitted with reasons. License selection is already per row.

[Private registration](../../internal/hostskills/sourcedregistration.go) writes regular marker
bytes and links supporting top-level entries into the sealed payload. The script fixture in
[the deleted-cache attach test](../../internal/cli/run/gitskillsubtrees_test.go) reads repository
siblings through its payload-backed script location. This does not prove that a relative link
in the private marker can read an unregistered sibling of its destination. Keep that distinction
in user documentation rather than promising universal sibling reachability.

Two confirmed adaptation seams:

1. Root raw imports must be distinguishable from manifest sourced rows, whose non-root
   validation stays intact.
2. Ordinary typed directories may lack a marker, but existing `SkillRequest` has only
   label/source/license and `validateSkillRow` requires marker bytes. Registration currently
   always authors a marker. Reusing it unchanged would silently narrow typed parity. The accepted
   [row-kind contract](../design/toml-scoped-pack-imports.md#row-kinds-and-typed-directory-eligibility)
   requires resolver-produced directory rows, no invented marker, explicit request/record state
   and strict hydration/downgrade validation, sharing the existing payload rather than another cache.

## Identity, ordering and lifetime

| Fact confirmed in source | Consequence |
| :--- | :--- |
| [Store tree identity](../../internal/packsrc/store.go) includes commit and subdirectory; [refresh stamps](../../internal/packsrc/refresh.go) use repository/ref | Selection must not become a shared cache identity. |
| [RefreshConfiguredPacks](../../internal/cli/run/packrefresh.go) runs before pack resolution; [Store.Refresh](../../internal/packsrc/refresh.go#L259-L290) saves successful pins first | A selector failure after refresh leaves the advanced pin. No previous-pin retention can be claimed. |
| [ResolveNeeds](../../internal/packload/needs.go) draws only from embedded official packs, and joins by name | Refuse a live need for a scoped shipped root before the existing already-selected no-op can mask it. |
| [NameCollisions](../../internal/packload/namecollisions.go) already compares namespace owners but separately refuses duplicate landings | Allowed same-handle scoped entries need inert landing handling; skill collisions still refuse under the common composer. |
| [ResolvePackForProcess](../../internal/config/packresolve.go#L269-L299) reads unfiltered local packs in place and filtered ones from a process tree | Scoped staged-copy carriers need process lifetime too, not a temporary directory removed before host delivery. |
| [The staged record](../../internal/packload/packtreerecord.go) preserves order/naming and sourced rows | New projection metadata must survive host/jail reload, not be reconstructed from host sources. |
| [HydrateSourcedSkills](../../internal/packload/skillpayloadtree.go) validates rows against staged declarations | Scoped entries need record-driven hydration that never loads the original manifest for delivery. |
| [Attach adoption](../../internal/cli/run/packtree.go) reads the running tree; [the entrypoint loader](../../internal/entrypoint/packsurfaces.go) reads the same record | Both readers must consume the new effective record and preserve source-free adoption. |

### Carrier closure facts checked

- **U-09:** [explicitRef](../../internal/packsrc/references.go#L240-L250) accepts nonempty strings,
  including a full SHA; [classifyRefAnd](../../internal/packsrc/refresh.go#L679-L695) explicitly
  recognizes a [40-digit hexadecimal commit](../../internal/packsrc/refresh.go#L736-L747).
  Known-good full-commit remediation is legal; this does not mean a refusal can guess a good pin.
- **U-10:** [SkillRecord](../../internal/packsrc/skillslock.go#L10-L24) is repository/ref-level and
  has no per-row publication state. Its `Kind` classifies the ref, **not** the row. Existing pure
  payload readers revalidate supplied rows. Keep kind/marker state in internal requests and staged
  rows, not selection history in the shared lock or stamp.
- **U-11:** [ordinary directory naming](../../internal/hostskills/deliver.go#L435-L477) calls
  `packname.Delivered(namespace, upstream)`, even without marker. The new row must match it.
- **Downgrade seam:** the baseline [tree record decoder](../../internal/packload/packtreerecord.go)
  uses ordinary JSON decoding without a version check. An extra unknown schema field alone
  would not make the old reader reject a scoped record. The plan requires a versioned scoped
  record shape that the actual previous decoder rejects, while new readers retain whole-pack
  sourced-record compatibility. This is a future acceptance requirement, not a baseline guarantee.

**Verdict:** preserve lock-first refresh, immutable repository payloads and one shared resolver;
add an inert effective projection, not another store or activation pipeline.

## Baseline test receipt and future evidence

The parser/naming subset below passed against the baseline with standard shared Go caches.
It proves existing behavior only; no feature test or runtime probe ran in this publication.

```bash
go env GOCACHE GOMODCACHE GOENV
go test -short ./internal/packsrc -run '^(TestParseSourceDefinitionsAndQualifiedPackReferences|TestParsePackReferenceAllowsOnlyKnownShippedBareName|TestParseDirectGitPackUsesRepositoryIdentityAndExplicitRef|TestParseDirectGitRejectsNestedRepositoryPath|TestParseSourceDefinitionsRejectsCredentialURLAndRawQuery|TestParsePackReferenceRejectsUnqualifiedAndUnknownHandles|TestTheWorkedExampleNamesEveryEntry|TestABarePackIsNamedExactlyAsBefore)$' -count=1
```

Result: `ok github.com/mschulkind-oss/yolo-jail/internal/packsrc 0.005s`.
Caches: `/home/agent/.cache/go-build`, `/home/agent/go/pkg/mod`; GOENV:
`/home/agent/.config/go/env`. HOME, TMPDIR and GOENV were not globally changed.

The [plan](../design/toml-scoped-pack-imports-plan.md) maps producer, closure, host composer,
immutable payload, refresh failure, staged reload, editing and migration tests. Future manual
fixtures belong in owned durable scratch, never a literal temporary root. Synthetic launcher
probes disable **both** program readiness and automatic installer capture; no real agent or
provider call is admissible.
