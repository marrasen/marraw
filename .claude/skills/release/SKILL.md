---
name: release
description: Cut a marraw release end-to-end — changelog from commits since the last tag, version bump by release size, commit, push, tag, watch the GitHub Actions build, publish the draft release. Use when the user says "Release new version" or asks to cut/ship/publish a release.
---

# Releasing a new marraw version

The pipeline: pushing a `v*` tag runs `.github/workflows/release.yml`, which
builds the Windows installer and uploads it to a **draft** GitHub release
(marrasen/marraw). Nothing reaches users — and electron-updater sees nothing —
until the draft is published. Publishing is the last step and it IS wanted:
"release" means users get the update.

## 1. Preflight

- Working tree must be clean and on `main`; `git fetch origin` and confirm
  `main` is not behind `origin/main`. Re-check `git log`/`git status` fresh —
  concurrent sessions may have committed since session start.
- `gh auth status` must show the marrasen account.
- Last release: `git describe --tags --abbrev=0` (also sanity-check
  `gh release list` — every existing tag should correspond to a published
  release; a lingering draft means a previous run died mid-way, finish it
  instead of starting over).

## 2. Changelog

- Collect changes: `git log v<last>..HEAD --oneline`, and read individual
  commits where the subject isn't self-explanatory.
- Write USER-FACING bullets — features, fixes, visible behavior; skip pure
  refactors, CI, docs, and test-harness work. Style: `Area: what changed`
  (the Welcome card renders the prefix before the first ": " in bold).
- Add a `## X.Y.Z - YYYY-MM-DD` section (today's date) at the TOP of
  `CHANGELOG.md`. The parser (client/src/lib/changelog.ts) only reads
  `## X.Y.Z - date` headers and `-` bullets; anything else is ignored.

## 3. Version

Decide from the size of the release (pre-1.0 conventions):

- **Patch** (0.1.0 → 0.1.1): only fixes / small polish, no new capability.
- **Minor** (0.1.0 → 0.2.0): any new feature or visible behavior change —
  the common case.
- **Major** (→ 1.0.0): never decide alone; ask the user first.

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

## 7. Confirm

`gh release view vX.Y.Z` shows `Draft: false` and the three assets. Report
the version, the changelog bullets, and the release URL to the user.
