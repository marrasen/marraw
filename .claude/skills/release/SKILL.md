---
name: release
description: Cut a marraw release end-to-end — changelog from commits since the last tag, version bump by release size, commit, push, tag, watch the GitHub Actions build, publish the draft release. Handles both stable releases (vX.Y.Z) and beta pre-releases (vX.Y.Z-beta.N). Use when the user says "Release new version" or asks to cut/ship/publish a release or beta.
---

# Releasing a new marraw version

The pipeline: pushing a `v*` tag runs `.github/workflows/release.yml`, which
builds the Windows installer and uploads it to a **draft** GitHub release
(marrasen/marraw). Nothing reaches users — and electron-updater sees nothing —
until the draft is published. Publishing is the last step and it IS wanted:
"release" means users get the update.

Development is trunk-based: everything happens on `main`, tags define
releases, there is no `develop` branch. If an old release ever needs a patch
while `main` has moved on, create `release/X.Y` retroactively from its tag.

## Channels: stable vs beta

Two channels ride the same pipeline:

- **Stable** — `vX.Y.Z`, published as the latest release. The default:
  "release" without qualification means stable.
- **Beta** — `vX.Y.Z-beta.N`, published as a GitHub **pre-release**. Use when
  the user says "beta". Pre-releases are excluded from `/releases/latest`
  (README install link keeps pointing at stable) and ignored by stable
  installs' electron-updater. An installed beta auto-tracks the channel:
  electron-updater turns on `allowPrerelease` when the running version has a
  prerelease suffix, so beta users get `beta.N+1` automatically and graduate
  to the final stable when it ships (semver: `0.5.0` > `0.5.0-beta.2`).
  Any install can also pin itself to the beta channel with the
  Settings → "Beta versions" toggle (shell pref `betaChannel` in
  preferences.json, see electron/main.cjs) — that's how a machine stays on
  the channel after a beta graduates to stable.

Where the flows differ is marked **[stable]** / **[beta]** below.

## 1. Preflight

- Working tree must be clean and on `main`; `git fetch origin` and confirm
  `main` is not behind `origin/main`. Re-check `git log`/`git status` fresh —
  concurrent sessions may have committed since session start.
- `gh auth status` must show the marrasen account.
- Last stable release: `git describe --tags --abbrev=0 --exclude='*-beta*'`.
  Last tag of any kind (for numbering the next beta):
  `git describe --tags --abbrev=0`. Also sanity-check `gh release list` —
  every existing tag should correspond to a published release; a lingering
  draft means a previous run died mid-way, finish it instead of starting over.

## 2. Changelog

- Collect changes: `git log v<last>..HEAD --oneline`, and read individual
  commits where the subject isn't self-explanatory. **[stable]** `v<last>` is
  the last STABLE tag — the section must also cover work already shipped in
  betas of this cycle. **[beta]** `v<last>` is the last tag of any kind
  (previous beta or last stable).
- Write USER-FACING bullets — features, fixes, visible behavior; skip pure
  refactors, CI, docs, and test-harness work. Style: `Area: what changed`
  (the Welcome card renders the prefix before the first ": " in bold).
- **[stable]** Add a `## X.Y.Z - YYYY-MM-DD` section (today's date) at the
  TOP of `CHANGELOG.md`. The parser (client/src/lib/changelog.ts) only reads
  `## X.Y.Z - date` headers and `-` bullets; anything else is ignored.
- **[beta]** Do NOT touch `CHANGELOG.md`. The parser cannot represent
  prerelease versions (`## X.Y.Z-beta.N` misparses: the version regex stops
  at `X.Y.Z` and `compareVersions` drops the suffix), and CHANGELOG.md is the
  stable-channel record. The bullets go only into the GitHub pre-release
  notes in step 6. Consequence (accepted): beta installs show no Welcome
  "What's new" card during the beta, and since `compareVersions` treats
  `X.Y.Z-beta.N` as equal to `X.Y.Z` they won't see the final stable's card
  either — the notes remain readable on the GitHub release.
- **[stable]** README check: the README is written for the *released*
  product and drifts while on `main`. Verify user-facing claims (status
  line, feature list, install instructions, version references) match what
  this release ships; fix them in the release commit.

## 3. Version

Decide from the size of the release (pre-1.0 conventions):

- **Patch** (0.1.0 → 0.1.1): only fixes / small polish, no new capability.
- **Minor** (0.1.0 → 0.2.0): any new feature or visible behavior change —
  the common case.
- **Major** (→ 1.0.0): never decide alone; ask the user first.

Beta versioning:

- **[beta]** First beta of a cycle: pick the target stable version by the
  rules above, then append `-beta.1` (e.g. 0.4.0 → `0.5.0-beta.1`). Later
  betas of the same cycle bump only N (`0.5.0-beta.2`). If the scope grows
  past the target mid-cycle (a beta cycle aimed at 0.5.1 gains a feature),
  restart numbering at the new target: `0.6.0-beta.1`.
- **[stable]** Closing a beta cycle: strip the suffix (`0.5.0-beta.2` →
  `0.5.0`).

Bump `version` in the ROOT `package.json` only. `client/package.json` is a
stale Vite-starter leftover — do not touch it. The CI guard fails the build
if the tag and package.json disagree, so they must move together.

## 4. Commit, push, tag

```
git add package.json CHANGELOG.md
git commit -m "Release vX.Y.Z"
git push origin main
git tag vX.Y.Z
git push origin vX.Y.Z
```

(The changelog section must be in the tagged commit — the Welcome "What's
new" card is bundled from the same tree the installer is built from.)

**[beta]** Same, but there is no CHANGELOG.md change to stage; commit message
`Release vX.Y.Z-beta.N`, tag `vX.Y.Z-beta.N`. The CI guard compares the tag
against package.json verbatim, so both must carry the suffix.

## 5. Monitor the pipeline

- `gh run list --workflow=release --limit 3` until the tag's run appears,
  then `gh run watch <id> --exit-status` (run in the background; the Windows
  job takes ~5–10 min with a warm LibRaw cache, 15+ cold).
- On FAILURE: `gh run view <id> --log-failed`, fix the cause on main, then
  move the tag to the fix and rerun:
  `git tag -d vX.Y.Z && git push origin :refs/tags/vX.Y.Z`, commit fix,
  re-tag, re-push. Delete the broken draft release if one was created.

## 6. Publish

When the run succeeds, electron-builder has created a draft release with
`marraw-Setup-X.Y.Z.exe`, `.blockmap`, and `latest.yml`. Verify the assets
(`gh release view vX.Y.Z`), then publish with the changelog section as notes:

```
gh release edit vX.Y.Z --draft=false --latest --notes-file <notes.md>
```

(Write the new CHANGELOG section's bullets to a temp notes file; don't dump
the whole changelog.) Publishing flips it live: installed apps auto-update on
their next launch (download in background, install on quit) and the Welcome
page shows the new section as "What's new".

**[beta]** Publish as a pre-release — NEVER `--latest` on a beta:

```
gh release edit vX.Y.Z-beta.N --draft=false --prerelease --notes-file <notes.md>
```

Notes come from the step-2 bullets (they exist nowhere else for a beta);
lead them with a line like "Beta for 0.5.0 — changes since v<last>". The
`--prerelease` flag is what keeps stable users unaffected; double-check it
took (`gh release view` shows `Pre-release: true`) before reporting done.

## 7. Confirm

`gh release view vX.Y.Z` shows `Draft: false` and the three assets (for a
beta, additionally `Pre-release: true`). Report the version, the changelog
bullets, and the release URL to the user.
