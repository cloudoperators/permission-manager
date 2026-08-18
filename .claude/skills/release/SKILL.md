---
name: release
description: >-
  Bump Chart.yaml appVersion to the latest published image tag and open a
  release PR. Use after /publish and the image workflow has completed. Use when
  the user asks to "release", "cut a release", or "open the chart bump PR".
---

# Release permission-manager chart

Bumps `Chart.yaml` and opens a PR. Requires the image to already be published
in GHCR (run `/publish` first and wait for the workflow to complete).

## Steps

### 1. Fetch latest image tag
- `git fetch --tags origin`
- `git tag --sort=-v:refname | grep -E '^v[0-9]' | head -1` → `<latest-tag>`

### 2. Check if chart already references latest tag
- Read `appVersion` from `charts/permission-manager/Chart.yaml`.
- If `appVersion` already equals `<latest-tag>`:
  - Tell the user: "Chart.yaml appVersion is already `<latest-tag>`. Nothing to
    bump."
  - Exit.

### 3. Verify image exists in GHCR
- `docker manifest inspect ghcr.io/cloudoperators/permission-manager:<latest-tag>`
- If it fails (image not yet published), tell the user and exit. Do not proceed
  until the image is confirmed in GHCR.

### 4. Resolve new chart version
- Read current `version` from `charts/permission-manager/Chart.yaml`.
- Ask the user: "Current chart version is <X.Y.Z>. Bump type — major, minor,
  or patch?"
- Increment accordingly:
  - patch: `X.Y.(Z+1)`
  - minor: `X.(Y+1).0`
  - major: `(X+1).0.0`

### 5. Check commit signing
- `git config --get user.signingkey`
- If not set, tell the user: "Commit signing is not configured. Set
  `user.signingkey` in your git config and re-run /release." Exit.

### 6. Bump Chart.yaml and open PR
1. Checkout a new branch:
   `git checkout -b release/permission-manager-<new-chart-version>`
2. Edit `charts/permission-manager/Chart.yaml`:
   - Set `appVersion` to `<latest-tag>` (quoted, e.g. `"v0.0.4"`).
   - Set `version` to `<new-chart-version>`.
3. Stage and commit with signing:
   `git add charts/permission-manager/Chart.yaml`
   `git commit -S -m "release(permission-manager): bump chart version from <old> to <new>"`
4. Push: `git push origin release/permission-manager-<new-chart-version>`
5. Open PR against `main`:
   - Try: `gh pr create --title "release(permission-manager): bump chart version from <old> to <new>" --body ""`
   - If `gh` is unavailable or not authenticated, print:
     "Open a PR manually at:
     https://github.com/cloudoperators/permission-manager/pull/new/release/permission-manager-<new-chart-version>"
6. Print the PR URL (or the manual URL above). End skill.
