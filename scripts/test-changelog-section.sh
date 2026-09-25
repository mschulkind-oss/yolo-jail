#!/bin/sh
# Tests for changelog-section.sh. Run by `just lint-ci` (and so by `just check-ci`
# and the pre-commit hook): the script is a release gate that three callers share
# — `just release`, release.yml and publish.yml — so its rules have to be pinned
# somewhere a change to them shows up as a diff.
#
# Adopted from Vantage's scripts/test-changelog-section.sh with the extractor.
# Every fixture case builds a whole fixture changelog rather than editing the
# real one: the interesting failures are about which section a heading belongs
# to, and that needs neighbors on both sides to be worth asserting. The last
# cases run against the repository's own CHANGELOG.md, which is what proves the
# default path and today's file shape still work.

set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
script="$here/changelog-section.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0

# The preamble is the real file's, so a fixture differs from CHANGELOG.md only in
# the part under test.
changelog() {
    {
        echo "# Changelog"
        echo ""
        echo "What changed in each release of YOLO Jail, newest first."
        echo ""
        cat
    } >"$tmp/CHANGELOG.md"
}

# expect <want-exit> <label> <version>
expect() {
    _want=$1
    _label=$2
    _version=$3
    _got=0
    "$script" "$_version" "$tmp/CHANGELOG.md" >"$tmp/out" 2>"$tmp/err" || _got=$?
    if [ "$_got" = "$_want" ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [$_label]: wanted exit $_want, got $_got"
        sed 's/^/      /' "$tmp/err"
    fi
}

# expect_says <text> <label> — the reason must name what is wrong, on stderr,
# because stdout is the payload the callers redirect.
expect_says() {
    if grep -qiF "$1" "$tmp/err"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [$2]: the failure never mentioned '$1'"
        sed 's/^/      /' "$tmp/err"
    fi
}

# expect_body <label> — stdout must equal $tmp/want, byte for byte.
expect_body() {
    if diff -u "$tmp/want" "$tmp/out" >"$tmp/diff"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [$1]: extracted body differs from what was written"
        sed 's/^/      /' "$tmp/diff"
    fi
}

# --- a good section, and only that section ------------------------------------

changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Added

- Six themes, and a picker that names the one on the page.

## [0.6.0] - 2026-09-10

### Added

- A neighbor that must not leak into 0.7.0.

## [0.5.4] - 2026-09-01
EOF

cat >"$tmp/want" <<'EOF'
### Added

- Six themes, and a picker that names the one on the page.
EOF

expect 0 "good section" 0.7.0
expect_body "good section, nothing of the neighbors"

expect 0 "middle section" 0.6.0
cat >"$tmp/want" <<'EOF'
### Added

- A neighbor that must not leak into 0.7.0.
EOF
expect_body "a section bounded on both sides"

# The last section in the file ends at end of file, not at a heading.
expect 1 "trailing heading with no body" 0.5.4
expect_says "empty" "trailing heading with no body"

# --- byte-for-byte, no reflowing ---------------------------------------------

# Two-space hard breaks, an indented continuation, a table, a very long line and
# a fenced block whose comment looks like a heading. A release body that
# re-wrapped any of this would render differently from the file it came from —
# and the fence is the case that once truncated a section at `# rebuild`.
printf '%s\n' \
'## [0.8.0] - 2026-10-01' \
'' \
'### Changed' \
'' \
'- The picker names the palette on the page.  ' \
'  It reads the resolved theme, not the stored preference.' \
'' \
'  | palette | contrast |' \
'  | ------- | -------- |' \
'  | Slate   | 7.1:1    |' \
'' \
'This paragraph is deliberately far longer than any reasonable fill column, because a release body that has been re-wrapped by the tool that extracted it is no longer the text anybody reviewed.' \
'' \
'```sh' \
'# rebuild the bundle' \
'just web-sync' \
'```' \
'' \
'## [0.7.0] - 2026-09-22' \
'' \
'### Added' \
'' \
'- A neighbor.' \
    | changelog

sed -n '5,$p' "$tmp/CHANGELOG.md" | sed -n '/^### Changed/,/^```$/p' >"$tmp/want"
expect 0 "fenced comment does not end the section" 0.8.0
expect_body "byte-identical, including hard breaks and the fence"

# --- headings it accepts -----------------------------------------------------

changelog <<'EOF'
## [v0.7.0] — 2026-09-22

The em dash, the bracket and the `v` are all cosmetic; the version is not.
EOF
expect 0 "v prefix and em dash" 0.7.0
expect 0 "a v on the argument too" v0.7.0

changelog <<'EOF'
## 0.7.0

No brackets and no date, which Keep a Changelog does not require.
EOF
expect 0 "bare version heading" 0.7.0

changelog <<'EOF'
##  [0.7.0]  - 2026-09-22

Extra spaces around the heading are cosmetic as well.
EOF
expect 0 "loose spacing" 0.7.0

# `###` is the subsection level inside a section, so it cannot also be a version
# heading — accepting both would make "where does this section end" ambiguous.
changelog <<'EOF'
### [0.7.0] - 2026-09-22

Written one level too deep.
EOF
expect 1 "level three is not a version heading" 0.7.0

# --- the version matches whole ----------------------------------------------

changelog <<'EOF'
## [0.7.0-rc1] - 2026-09-20

A release candidate, which is not the release.

## [0.17.0] - 2026-09-18

A later minor whose number contains no substring of 0.7.0 either way round.
EOF
expect 1 "0.7.0 does not match 0.7.0-rc1 or 0.17.0" 0.7.0
expect 0 "the rc can be asked for by its full version" 0.7.0-rc1
expect 0 "and so can the later minor" 0.17.0

changelog <<'EOF'
## [10.7.0] - 2026-09-18

A major version that ends with the version being asked for.
EOF
expect 1 "0.7.0 does not match 10.7.0" 0.7.0

# A retrospective heading summarizes a whole minor line written after the fact.
# It must never be found by a release of that line: `just release 0.9.0` would
# otherwise publish the summary as the notes of a new tag. `0.9.x` is safe
# because the version must end at `]`, whitespace or end of line.
changelog <<'EOF'
## 0.9.x

_0.9.0, September 2026._ A summary of the line.
EOF
expect 1 "0.9.0 does not match a 0.9.x retrospective" 0.9.0
expect 1 "0.9 does not match a 0.9.x retrospective" 0.9

# --- absent, empty, placeholder ---------------------------------------------

changelog <<'EOF'
## [0.6.0] - 2026-09-10

### Added

- Something real, for a version nobody asked about.
EOF
expect 1 "missing version" 0.7.0
expect_says "no section for 0.7.0" "missing version"

changelog <<'EOF'
## [0.7.0] - 2026-09-22

## [0.6.0] - 2026-09-10

- Something real.
EOF
expect 1 "empty section" 0.7.0
expect_says "empty" "empty section"

# Headings with nothing under them are as empty as nothing at all, and saying so
# is more useful than handing GitHub three bare subheadings.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Added

### Fixed
EOF
expect 1 "headings but no content" 0.7.0
expect_says "no content" "headings but no content"

for stub in "TBD" "- TODO" "_Coming soon._" "- Nothing yet" "..."; do
    changelog <<EOF
## [0.7.0] - 2026-09-22

### Added

$stub
EOF
    expect 1 "placeholder: $stub" 0.7.0
    expect_says "placeholder" "placeholder: $stub"
done

# The stub a maintainer leaves themselves, in a comment.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Added

<!-- write this before tagging -->
EOF
expect 1 "placeholder in an HTML comment" 0.7.0

# One real line among the stubs is prose being written, not a placeholder. This
# is the boundary the check has to get right, or it fails an honest release.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Added

- Six themes, and a picker that names the one on the page.
- TODO: the second half of this list
EOF
expect 0 "a stub beside real prose is not a placeholder section" 0.7.0

# --- the filler list --------------------------------------------------------
#
# One line per phrase changelog-section.sh refuses. A phrase added there without
# a line here is a rule nothing pins down.
for slop in \
    "Various improvements to the viewer." \
    "Various fixes." \
    "Miscellaneous fixes across the frontend." \
    "Misc. changes to the picker." \
    "Bug fixes and improvements." \
    "Bug fixes and other improvements." \
    "This release includes a new theme picker." \
    "The release brings a new theme picker." \
    "We're excited to ship six new palettes." \
    "We are pleased to announce the theme picker." \
    "Excited to announce six new palettes." \
    "Under the hood, preferences moved to one layer." \
    "Six new palettes and much more." \
    "Six new palettes and lots more." \
    "Six new palettes and more!" \
    "**Full Changelog**: https://github.com/mschulkind-oss/yolo-jail/compare/v0.9.0...v0.10.0" \
    ; do
    changelog <<EOF
## [0.7.0] - 2026-09-22

### Added

- A theme picker that names the palette on the page.

$slop
EOF
    expect 1 "filler: $slop" 0.7.0
done

# The reason has to name the phrase that matched — a bare "rejected" teaches
# nothing, and the phrase is the whole lesson.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

Various improvements to the viewer.
EOF
expect 1 "filler names itself" 0.7.0
expect_says "various improvements" "filler names itself"

# Words from the list used in honest sentences. A check that fires on these is
# worse than no check, so they are asserted to pass.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Changed

- The theme picker reads the resolved palette rather than the stored preference,
  so the two tabs of a split window can no longer disagree about what is on the
  page. Various parts of the preference layer moved with it: `usePreference` is
  now the only writer, and the full changelog of that refactor is in the
  preferences design doc.
- Contrast improved on Solarized Light, which sat under the 4.5:1 floor.
EOF
expect 0 "honest prose using words from the filler list" 0.7.0

# --- a dumped git log -------------------------------------------------------

changelog <<'EOF'
## [0.7.0] - 2026-09-22

- feat(viewer): the theme picker names what is on the page
- feat(viewer): one preference layer, and every preference follows the reader
- refactor(viewer): name the built-in look Slate
- feat(themes): ship Nord, Gruvbox, Solarized and Tokyo Night
EOF
expect 1 "commit subjects" 0.7.0
expect_says "commit subjects" "commit subjects"

# `--generate-notes`' own shape, which is the thing being replaced.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

* fix: stop the picker flashing by @someone in #12
* chore(deps): bump vite by @dependabot in #13
EOF
expect 1 "generated notes pasted in" 0.7.0

# A single bullet that is a commit subject is still a commit subject.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

- feat(viewer): the theme picker names what is on the page
EOF
expect 1 "one commit subject, alone" 0.7.0

# Bullets alone are a shape this repo's changelog has used, so the rule is about
# commit subjects, not bullets.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Fixed

- `just build` now works after `git pull` without needing `just setup`.
- `just setup` no longer modifies lockfiles.
EOF
expect 0 "hand-written bullets only" 0.7.0

# And one bullet that happens to open like a commit subject does not make a
# hand-written list into a git log.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

### Fixed

- The picker no longer flashes on first paint.
- Solarized Light now clears the contrast floor.
- fix: the stored preference is migrated, not dropped.
- Tokyo Night's code background matches its prose background.
EOF
expect 0 "one commit-shaped bullet in a majority of prose" 0.7.0

# --- --link-base: relative links, made absolute for the release page ---------
#
# A GitHub release body is rendered at /releases/tag/<tag>, not inside the tree,
# so a repository-relative link there resolves under /releases/tag/ and 404s.
# Every shape the rewrite must touch, and every shape it must not, in one section.

base=https://github.com/o/r/blob/v0.7.0

changelog <<'EOF'
## [0.7.0] - 2026-09-22

See [Themes](userguide/guides/themes.md) and [Starred](userguide/features.md#starred).
Rooted [one](/docs/a.md), dotted [two](./docs/b.md), same file [three](#060).
Already absolute: [site](https://example.com/x) and [mail](mailto:a@b.c).
In a code span it is text: `[x](userguide/not-a-link.md)`, then [real](docs/c.md).

[ref]: userguide/reference.md
[abs]: https://example.com/y "a title"

```md
[fenced](userguide/also-text.md)
```
EOF

cat >"$tmp/want" <<'EOF'
See [Themes](https://github.com/o/r/blob/v0.7.0/userguide/guides/themes.md) and [Starred](https://github.com/o/r/blob/v0.7.0/userguide/features.md#starred).
Rooted [one](https://github.com/o/r/blob/v0.7.0/docs/a.md), dotted [two](https://github.com/o/r/blob/v0.7.0/docs/b.md), same file [three](https://github.com/o/r/blob/v0.7.0/CHANGELOG.md#060).
Already absolute: [site](https://example.com/x) and [mail](mailto:a@b.c).
In a code span it is text: `[x](userguide/not-a-link.md)`, then [real](https://github.com/o/r/blob/v0.7.0/docs/c.md).

[ref]: https://github.com/o/r/blob/v0.7.0/userguide/reference.md
[abs]: https://example.com/y "a title"

```md
[fenced](userguide/also-text.md)
```
EOF

_got=0
"$script" --link-base "$base" 0.7.0 "$tmp/CHANGELOG.md" >"$tmp/out" 2>"$tmp/err" || _got=$?
if [ "$_got" = 0 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL [--link-base]: wanted exit 0, got $_got"; sed 's/^/      /' "$tmp/err"; fi
expect_body "--link-base rewrites relative links and only those"

# The trailing slash is normalized, so either spelling joins to one slash.
"$script" --link-base "$base/" 0.7.0 "$tmp/CHANGELOG.md" >"$tmp/out" 2>/dev/null || true
expect_body "--link-base with a trailing slash"

# Without the option the body is still byte-for-byte what was written.
sed -n '7,$p' "$tmp/CHANGELOG.md" >"$tmp/want"
expect 0 "no --link-base" 0.7.0
expect_body "no --link-base leaves every link as written"

_got=0
"$script" --link-base >/dev/null 2>&1 || _got=$?
if [ "$_got" = 2 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL [--link-base with no url]: wanted exit 2, got $_got"; fi

# --- --unwrap: joined paragraphs, for a page that renders newlines as <br> ---
#
# GitHub renders a release body with every newline a hard break, which is how
# v0.7.0 went out as a ragged 80-column strip. Every block whose line breaks
# carry meaning has to come through untouched, and only prose is joined.

printf '%s\n' \
'## [0.7.0] - 2026-09-22' \
'' \
'A paragraph wrapped' \
'at a narrow column,' \
'  with stray indentation.' \
'' \
'- A list item whose text' \
'  hangs under the marker.' \
'- A second item.' \
'  - A nested item, which' \
'    wraps too.' \
'' \
'1. Numbered, and' \
'   wrapped.' \
'2. Second.' \
'' \
'A sentence that wraps just before a year' \
'2026. is still one sentence.' \
'' \
'A hard break here.  ' \
'And a backslash one.\' \
'Then prose that' \
'joins again.' \
'' \
'| a | b |' \
'| - | - |' \
'| 1 | 2 |' \
'' \
'> Quoted' \
'> lines.' \
'' \
'<!-- a comment' \
'     over two lines -->' \
'' \
'    indented code' \
'    stays put' \
'' \
'```sh' \
'# a fenced' \
'# block' \
'```' \
'' \
'### Fixed' \
'Straight after a heading,' \
'still a paragraph.' \
'' \
'[ref]: userguide/a.md' \
'[two]: userguide/b.md' \
    | changelog

printf '%s\n' \
'A paragraph wrapped at a narrow column, with stray indentation.' \
'' \
'- A list item whose text hangs under the marker.' \
'- A second item.' \
'  - A nested item, which wraps too.' \
'' \
'1. Numbered, and wrapped.' \
'2. Second.' \
'' \
'A sentence that wraps just before a year 2026. is still one sentence.' \
'' \
'A hard break here.  ' \
'And a backslash one.\' \
'Then prose that joins again.' \
'' \
'| a | b |' \
'| - | - |' \
'| 1 | 2 |' \
'' \
'> Quoted' \
'> lines.' \
'' \
'<!-- a comment' \
'     over two lines -->' \
'' \
'    indented code' \
'    stays put' \
'' \
'```sh' \
'# a fenced' \
'# block' \
'```' \
'' \
'### Fixed' \
'Straight after a heading, still a paragraph.' \
'' \
'[ref]: userguide/a.md' \
'[two]: userguide/b.md' \
    >"$tmp/want"

_got=0
"$script" --unwrap 0.7.0 "$tmp/CHANGELOG.md" >"$tmp/out" 2>"$tmp/err" || _got=$?
if [ "$_got" = 0 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL [--unwrap]: wanted exit 0, got $_got"; sed 's/^/      /' "$tmp/err"; fi
expect_body "--unwrap joins prose and keeps every meaningful line break"

# Both options together, in either order: links are rewritten and then joined.
changelog <<'EOF'
## [0.7.0] - 2026-09-22

See the
[Themes](userguide/themes.md) guide.
EOF
echo "See the [Themes](https://github.com/o/r/blob/v0.7.0/userguide/themes.md) guide." >"$tmp/want"
"$script" --unwrap --link-base "$base" 0.7.0 "$tmp/CHANGELOG.md" >"$tmp/out" 2>/dev/null || true
expect_body "--unwrap then --link-base"
"$script" --link-base "$base" --unwrap 0.7.0 "$tmp/CHANGELOG.md" >"$tmp/out" 2>/dev/null || true
expect_body "--link-base then --unwrap"

_got=0
"$script" --no-such-option 0.7.0 >/dev/null 2>&1 || _got=$?
if [ "$_got" = 2 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL [unknown option]: wanted exit 2, got $_got"; fi

# --- usage errors are not policy failures ------------------------------------

_got=0
"$script" >/dev/null 2>&1 || _got=$?
if [ "$_got" = 2 ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [no arguments]: wanted exit 2, got $_got"
fi

_got=0
"$script" 0.7.0 "$tmp/does-not-exist" >/dev/null 2>&1 || _got=$?
if [ "$_got" = 2 ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [unreadable file]: wanted exit 2, got $_got"
fi

# --- the real file, through the default path ---------------------------------

# No file argument: the default is the changelog at the repository root, which
# is what every caller relies on — `just release` runs from the repo root and
# the workflows from a checkout of the tag, and none of them passes a path.
#
# Which sections are asked for is read off the file rather than written here, so
# a release that renames [Unreleased] or folds a section into a retrospective
# cannot leave this pointing at a heading that is gone:
#
#   - every bracketed heading, `## [<version>]` or `## [Unreleased]`, must
#     extract cleanly, except an [Unreleased] with nothing under it yet (the
#     state straight after a release renames it);
#   - at least one must, or the default path was never exercised;
#   - every retrospective heading, `## <major>.<minor>.x`, must NOT extract as
#     `<major>.<minor>.0`, because that is the one mistake that would publish
#     a summary of an old line as the notes of a new tag.
real_file="$here/../CHANGELOG.md"
extracted=0
for heading in $(sed -n 's/^##[[:space:]]*\[\([^]]*\)\].*/\1/p' "$real_file"); do
    _got=0
    "$script" "$heading" >"$tmp/out" 2>"$tmp/err" || _got=$?
    if [ "$_got" = 0 ] && [ -s "$tmp/out" ]; then
        pass=$((pass + 1))
        extracted=$((extracted + 1))
    elif [ "$heading" = Unreleased ] && grep -qi "empty" "$tmp/err"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [real CHANGELOG.md, section $heading]: exit $_got"
        sed 's/^/      /' "$tmp/err"
    fi
done
if [ "$extracted" -ge 1 ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [real CHANGELOG.md]: no bracketed section extracted through the default path"
fi

# And no third shape: a level-two heading that is neither bracketed nor `X.Y.x`
# is either a typo'd release heading or a bare `## 0.9`, which the extractor
# would hand out for `0.9`.
stray=$(grep -E '^##[[:space:]]' "$real_file" \
    | grep -Ev '^##[[:space:]]*\[[^]]+\]' \
    | grep -Ev '^##[[:space:]]*[0-9]+\.[0-9]+\.x[[:space:]]*$' || true)
if [ -z "$stray" ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [real CHANGELOG.md]: a heading is neither [<version>] nor <major>.<minor>.x:"
    printf '%s\n' "$stray" | sed 's/^/      /'
fi

for line in $(sed -n 's/^##[[:space:]]*\([0-9][0-9]*\.[0-9][0-9]*\)\.x[[:space:]]*$/\1/p' "$real_file"); do
    _got=0
    "$script" "$line.0" >/dev/null 2>&1 || _got=$?
    if [ "$_got" = 1 ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [real CHANGELOG.md, retrospective $line.x]: '$line.0' extracted (exit $_got)"
    fi
done

# --- the callers --------------------------------------------------------------

# A gate nobody calls is not a gate, and every case above would stay green with
# every caller deleted. So the three call sites are pinned here, read as text so
# the check needs neither `just` nor a workflow runner.
root="$here/.."

# expect_file <label> <file> <extended-regex> — a caller that must be there.
expect_file() {
    if grep -Eq -- "$3" "$2"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [$1]: $2 no longer matches: $3"
    fi
}

# `just release` runs the gate, and runs it BEFORE it tags anything.
awk '
    /^release[[:space:]]/ { inside = 1; next }
    inside && /^[^[:space:]#]/ { inside = 0 }
    inside { print }
' "$root/Justfile" >"$tmp/release-recipe"
gate_line=$(grep -n 'scripts/changelog-section.sh "{{version}}"' "$tmp/release-recipe" | head -1 | cut -d: -f1)
tag_line=$(grep -n 'git tag ' "$tmp/release-recipe" | head -1 | cut -d: -f1)
if [ -n "$gate_line" ] && [ -n "$tag_line" ] && [ "$gate_line" -lt "$tag_line" ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [just release gates before tagging]: gate at line '${gate_line:-none}', tag at line '${tag_line:-none}' of the recipe"
fi

# release.yml publishes the extracted section as the body, links pinned to the tag.
expect_file "release.yml extracts the section" "$root/.github/workflows/release.yml" \
    'sh scripts/changelog-section.sh --link-base "\$base" --unwrap "\$version" > "\$RUNNER_TEMP/notes.md"'
expect_file "release.yml hands it to goreleaser" "$root/.github/workflows/release.yml" \
    'args: release --clean --release-notes \$\{\{ runner.temp \}\}/notes.md'

# publish.yml refuses to ship PyPI wheels or images for a tag with no section.
expect_file "publish.yml gates on the section" "$root/.github/workflows/publish.yml" \
    'sh scripts/changelog-section.sh "\$version"'

# ungated_jobs <workflow> <gate-job> — print every job whose `needs:` chain does
# not reach <gate-job>. The gate job existing is not the gate: a job that does
# not wait on it runs in parallel and publishes whatever the section says, so
# every OTHER job must reach it, including a root job added later. Reads the
# three `needs:` spellings (scalar, flow list, block list) of a two-space-indented
# `jobs:` map, which is the only shape these workflows use.
ungated_jobs() {
    awk -v gate="$2" '
        /^jobs:[[:space:]]*$/ { injobs = 1; next }
        injobs && /^[^[:space:]#]/ { injobs = 0 }
        !injobs { next }
        /^  [A-Za-z0-9_-]+:[[:space:]]*(#.*)?$/ {
            job = $1; sub(/:$/, "", job); jobs[++n] = job; inneeds = 0; next
        }
        inneeds && /^      +- / {
            dep = $0; sub(/^ *- */, "", dep); sub(/[[:space:]#].*$/, "", dep)
            needs[job] = needs[job] " " dep; next
        }
        inneeds && !/^[[:space:]]*(#.*)?$/ { inneeds = 0 }
        /^    needs:/ {
            v = $0; sub(/^    needs:[[:space:]]*/, "", v); sub(/[[:space:]]*#.*$/, "", v)
            if (v == "") { inneeds = 1; next }
            gsub(/[][,]/, " ", v); needs[job] = needs[job] " " v
        }
        END {
            reach[gate] = 1
            do {
                changed = 0
                for (i = 1; i <= n; i++) {
                    j = jobs[i]
                    if (reach[j]) continue
                    m = split(needs[j], d, /[[:space:]]+/)
                    for (k = 1; k <= m; k++)
                        if (d[k] != "" && reach[d[k]]) { reach[j] = 1; changed = 1; break }
                }
            } while (changed)
            found = 0
            for (i = 1; i <= n; i++) if (jobs[i] == gate) found = 1
            if (!found) print "(no job named " gate ")"
            for (i = 1; i <= n; i++) if (!reach[jobs[i]]) print jobs[i]
        }
    ' "$1"
}

# expect_all_gated <label> <workflow> <gate-job>
expect_all_gated() {
    _ungated=$(ungated_jobs "$2" "$3")
    if [ -z "$_ungated" ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [$1]: these jobs of $2 do not wait on '$3':"
        printf '%s\n' "$_ungated" | sed 's/^/      /'
    fi
}

expect_all_gated "every publish.yml job waits on release-notes" \
    "$root/.github/workflows/publish.yml" release-notes
# release.yml extracts the notes inside its goreleaser job, so that job is the gate.
expect_all_gated "every release.yml job waits on goreleaser" \
    "$root/.github/workflows/release.yml" goreleaser

# The reachability reader itself, on fixtures: it must see a job that waits on
# nothing, and must accept all three `needs:` spellings as waiting.
cat >"$tmp/wf-gated.yml" <<'EOF'
on: push
jobs:
  gate:
    runs-on: x
  a:
    needs: gate
  b:
    needs: [a, gate]
  c:
    needs:
      - b
  d:  # a comment
    needs: [c]  # another
EOF
cat >"$tmp/wf-ungated.yml" <<'EOF'
jobs:
  gate:
    runs-on: x
  a:
    needs: gate
  loose:
    runs-on: x
  after-loose:
    needs:
      - loose
EOF
if [ -z "$(ungated_jobs "$tmp/wf-gated.yml" gate)" ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [ungated_jobs fixture]: reported a gated job: $(ungated_jobs "$tmp/wf-gated.yml" gate | tr '\n' ' ')"
fi
_got=$(ungated_jobs "$tmp/wf-ungated.yml" gate | tr '\n' ' ')
if [ "$_got" = "loose after-loose " ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [ungated_jobs fixture]: want 'loose after-loose ', got '$_got'"
fi
_got=$(ungated_jobs "$tmp/wf-gated.yml" missing | head -1)
if [ "$_got" = "(no job named missing)" ]; then
    pass=$((pass + 1))
else
    fail=$((fail + 1))
    echo "FAIL [ungated_jobs fixture]: a missing gate job went unreported (got '$_got')"
fi

# `changelog: {disable: true}` would skip goreleaser's changelog pipe, and that
# pipe is what loads --release-notes: the release would ship with no body.
# goreleaser evaluates `disable` as a template (`tmpl.Bool`), so ANY value can
# turn out true — `"{{ true }}"`, an env lookup — and the only safe config names
# no `disable` at all. A flow mapping on the `changelog:` line itself is refused
# outright, since this reader cannot see inside it.
changelog_disable_set() {
    awk '
        /^changelog:[[:space:]]*[^[:space:]#]/ { found = 1 }
        /^changelog:/ { inside = 1; next }
        inside && /^[^[:space:]#]/ { inside = 0 }
        inside && /^[[:space:]]+"?disable"?[[:space:]]*:/ { found = 1 }
        END { exit(found ? 0 : 1) }
    ' "$1"
}
if changelog_disable_set "$root/.goreleaser.yaml"; then
    fail=$((fail + 1))
    echo "FAIL [.goreleaser.yaml]: changelog names a disable key (or is a flow mapping), which can drop --release-notes"
else
    pass=$((pass + 1))
fi
for _spelling in \
    'changelog:\n  disable: true\n' \
    'changelog:\n  disable: "{{ true }}"\n' \
    'changelog:\n  disable: false\n' \
    'changelog:\n  sort: asc\n  "disable": true\n' \
    'changelog: {disable: true}\n' \
    'changelog: { sort: asc }\n'; do
    printf 'version: 2\n'"$_spelling"'release:\n  draft: false\n' >"$tmp/goreleaser.yaml"
    if changelog_disable_set "$tmp/goreleaser.yaml"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL [changelog disable guard]: missed $(printf '%s' "$_spelling")"
    fi
done
for _spelling in \
    '' \
    'changelog:\n  sort: asc\n' \
    'changelog:  # comment\n  use: git\nrelease:\n  disable: true\n'; do
    printf 'version: 2\n'"$_spelling"'release:\n  draft: false\n' >"$tmp/goreleaser.yaml"
    if changelog_disable_set "$tmp/goreleaser.yaml"; then
        fail=$((fail + 1))
        echo "FAIL [changelog disable guard]: refused a config with no changelog.disable: $(printf '%s' "$_spelling")"
    else
        pass=$((pass + 1))
    fi
done

# --- `just release`, run -------------------------------------------------------

# The recipe's refusals are behavior, not text, so they are exercised: the real
# Justfile and extractor in a scratch repo whose `origin` is a local bare repo.
# The case that matters most is the one a text check cannot see: a commit that
# some OTHER remote has, but origin does not, must not be tagged on origin.
if command -v just >/dev/null 2>&1 && command -v git >/dev/null 2>&1; then
    rr="$tmp/release"
    mkdir -p "$rr/work/scripts"
    (
        # Git's own state from a hook (a pre-commit in a linked worktree exports an
        # ABSOLUTE GIT_DIR and GIT_INDEX_FILE) would point every git below at the
        # committer's repository; the same list packsrc.CleanGitEnv strips.
        unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY \
            GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_COMMON_DIR GIT_NAMESPACE GIT_PREFIX \
            GIT_CEILING_DIRECTORIES
        # No user or system config: a signing or hook setting there must not
        # decide this test, and the tag needs an identity of its own.
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
        export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
        export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
        git init -q --bare "$rr/origin.git"
        git init -q --bare "$rr/other.git"
        cd "$rr/work"
        git init -q -b main .
        cp "$root/Justfile" Justfile
        cp "$script" scripts/changelog-section.sh
        cat >CHANGELOG.md <<'EOF'
# Changelog

## [0.11.1] - 2026-09-25

A launch now says which flake it built from, so a stale checkout is visible before an agent runs.

### Fixed

- `yolo check --no-build` no longer builds the image when the nix daemon is slow to answer.
EOF
        git add -A && git commit -qm "release 0.11.1"
        git remote add origin "$rr/origin.git"
        git remote add other "$rr/other.git"

        # 0. No section for the version: refused before anything else is asked.
        git push -q origin main
        if just release 0.11.2 >"$rr/out" 2>&1; then
            echo "UNGATED-nosection"
        elif git ls-remote --tags origin | grep -q 'v0.11.2'; then
            echo "TAGGED-nosection"
        else
            echo "ok-nosection"
        fi
        git --git-dir="$rr/origin.git" branch -q -D main
        git fetch -q --prune origin

        # 1. The commit is on `other` only: refused, and no tag reaches origin.
        git push -q other main
        if just release 0.11.1 >"$rr/out" 2>&1; then
            echo "UNGATED-other"
        elif git ls-remote --tags origin | grep -q 'v0.11.1'; then
            echo "TAGGED-other"
        else
            echo "ok-other"
        fi
        # Undo a tag a broken recipe made, so one failure does not cascade.
        git tag -d v0.11.1 >/dev/null 2>&1 || true
        git push -q origin --delete v0.11.1 >/dev/null 2>&1 || true

        # 2. Origin had the commit only on a branch it has since deleted, and the
        # local tracking ref still names it: `--prune` must retire that ref.
        # Deleted on origin's side directly: a `git push --delete` from here would
        # retire the tracking ref itself and leave nothing for the prune to do.
        git push -q origin main:gone
        git --git-dir="$rr/origin.git" branch -q -D gone
        if just release 0.11.1 >"$rr/out" 2>&1; then
            echo "UNGATED-stale"
        else
            echo "ok-stale"
        fi
        # Undo a tag a broken recipe made, so one failure does not cascade.
        git tag -d v0.11.1 >/dev/null 2>&1 || true
        git push -q origin --delete v0.11.1 >/dev/null 2>&1 || true

        # 3. On origin's main: tagged, and the tag is on origin.
        git push -q origin main
        if just release 0.11.1 >"$rr/out" 2>&1 \
            && git ls-remote --tags origin | grep -q 'refs/tags/v0.11.1'; then
            echo "ok-origin"
        else
            echo "REFUSED-origin"
        fi
    ) >"$tmp/release-results" 2>"$tmp/release-err"
    for _case in nosection other stale origin; do
        if grep -qx "ok-$_case" "$tmp/release-results"; then
            pass=$((pass + 1))
        else
            fail=$((fail + 1))
            echo "FAIL [just release, HEAD $_case]: got '$(grep -- "-$_case\$" "$tmp/release-results" || echo 'no result')'"
            sed 's/^/      /' "$rr/out" "$tmp/release-err"
        fi
    done
else
    echo "SKIP [just release, run]: needs both just and git on PATH"
fi

# --- result ------------------------------------------------------------------

echo ""
if [ "$fail" -ne 0 ]; then
    echo "changelog-section: $pass passed, $fail FAILED"
    exit 1
fi
echo "changelog-section: $pass passed"
