---
name: publish
description: >-
  Publish a new Docker image for permission-manager by pushing a semver tag.
  Checks if HEAD is already tagged, prompts for bump type (major/minor/patch),
  confirms with user, then pushes the tag. Use when the user asks to "publish",
  "publish the image", or "tag the image".
---

# Publish permission-manager image

Pushes a `v*.*.*` tag to trigger the Docker image workflow. Ends after the tag
is pushed — does not wait for CI.

## Steps

### 1. Check if HEAD is already tagged
- `git fetch origin main`
- `git rev-parse HEAD` to get the HEAD SHA.
- `git tag --points-at HEAD` to list tags on HEAD.
- If a `v*.*.*` tag already points at HEAD:
  - Tell the user the existing tag (e.g. `v0.0.3 already points at this commit`).
  - Ask: "The image tag for this commit already exists. Do you want to skip to
    /release for the chart bump?"
  - Exit. Do not push any tag.

### 2. Resolve the next tag
- Read the latest `v*.*.*` tag: `git tag --sort=-v:refname | grep -E '^v[0-9]' | head -1`.
- Ask the user: "Latest tag is <vX.Y.Z>. Bump type — major, minor, or patch?"
- Increment accordingly:
  - patch: `vX.Y.(Z+1)`
  - minor: `vX.(Y+1).0`
  - major: `v(X+1).0.0`
- Show the resolved new tag and confirm: "Tag `<new>` at commit `<sha>` — proceed?"
- If user says no, exit.

### 3. Push the tag
- `git tag <new-tag>`
- `git push origin <new-tag>`
- Print: "Tag `<new-tag>` pushed. The image workflow is now running. Run
  `/release` once the image is published to GHCR."
- End skill.