---
name: release
description: >
  Release the Kairos private journal through a CalVer tag: verify the candidate,
  push an authorized tag, watch Kairos Image, and verify the deployed
  source commit and image digest. Use for an explicitly requested Kairos release
  or release candidate; changing release configuration is not itself a release.
metadata:
  owner: "EgoSay"
  version: "2.1.0"
  last-reviewed: "2026-10-10"
---

# Kairos release

Pushing a `YY.MM[.N][-rc.N]` tag triggers `.github/workflows/journal-image.yml`.
The workflow validates the version, runs frontend/backend checks, builds and
smoke-tests the complete image, publishes to `ghcr.io/egosay/kairos`,
verifies the pre-upgrade snapshot/R2 backup, and updates the pinned
`production/memos-journal` declaration for Dokploy. It confirms the public
source identity and server image health before completing the deployment.

The upstream Canary, Release, Render demo and stale-item workflows are retired.
Do not invoke `release.yml`, publish to `neosmemo/memos` or
`ghcr.io/usememos/memos`, or claim Kairos builds upstream binary archives.
The journal workflow does not create a GitHub Release page.
Before the first Kairos release, verify that Dokploy uses `EgoSay/kairos`
and the deployed `journal_release_gate.py` expects the Kairos registry.
Keep the existing composeId, appName, production branch, volumes, and data paths.
New GHCR packages default to private. This deployment uses public images:
after the first package push, confirm its visibility is Public. The workflow
checks anonymous manifest access before freezing writes. If that check fails,
configure the package visibility and rerun only after confirming preparation
never started; publishing credentials do not prove Dokploy pull access.
Already published tags retain legacy request compatibility. New Kairos requests
include the repository and must confirm the exact requested repository and digest.
Both approved repositories remain usable as previous images for rollback.

## Candidate and authorization

- Inspect the actual remote refs and existing tags. Default to a reviewed
  `origin/main` commit unless the user specified another candidate. Any branch
  is supported; being reachable from main is not a release requirement.
- The candidate must contain the current tag-triggered journal workflow and
  the removal of upstream release workflows. An old commit may still contain
  old branch/tag triggers, so do not treat its configuration as current.
- Use an explicit pushed commit SHA, never uncommitted working-tree contents.
  Do not stash, rebase or discard user changes to prepare a release.
- Apply the user's existing authorization. A request to adjust the workflow
  does not authorize publishing a version tag or deploying the application.
  If release authorization is missing, prepare the candidate, tag and checks
  before requesting it. Do not ask again if the release is already authorized.
- Both stable and `-rc.N` tags use the production pipeline. An RC is not a
  preview environment or a dry run.
- Never delete, move or force-push an existing version tag without explicit
  authorization for that tag. Prefer a new version after a source correction.

## Validate and publish a tag

1. Fetch the relevant remote refs and inspect tags. Validate the chosen version
   with `bash scripts/release_version.sh version <tag>`; use the existing
   `YY.MM[.N][-rc.N]` convention, such as `26.10.1` or `26.10-rc.1`.
2. Confirm the tag is unused and points to the intended reviewed commit.
   Inspect available CI results for that exact commit; run relevant checks
   when missing. Upgrade checks can be run through `upgrade-smoke.yml` on the
   candidate ref. There is no manual dispatch for the production workflow.
3. Check the current production declaration and the required deployment
   credentials without displaying secret values. Missing deployment credentials
   cause the workflow to publish only the image, so a green run alone is not
   proof of an application deployment.
4. With release authorization, create a lightweight or annotated tag on the
   explicit commit and push only that tag. The workflow resolves the checked-out
   commit for both tag types and embeds the tag as the application version.

```bash
git tag -a VERSION REVIEWED_COMMIT -m "Kairos VERSION"
git push origin refs/tags/VERSION
```

## Observe and verify

Find the `journal-image.yml` push run matching the tag. Verify the tag, resolved
commit, image version and digest agree. Confirm the production declaration,
public profile and server deployment receipt identify that commit and digest,
and that backup scheduling resumed. The workflow publishes version, commit and
`production` image tags; deployment uses the immutable digest.

Do not equate an image publication, HTTP response, or green build with verified
production deployment. Distinguish the workflow configuration check, actual
release execution, server confirmation, and user product acceptance.

## Failure and recovery

Read the failed step and private receipts before deciding the next action.
Rerun a transiently failed job only when the release state makes it safe; do not
blindly retry backup/prepare steps while a pending release exists. For a source
change, use a newly reviewed commit and a new version tag.

Follow `scripts/README.md` for rollback. The pending-release gate protects new
writes and schema changes; revert the production declaration only after safe
data rollback is confirmed, because that push triggers Dokploy. Confirmed-release
recovery requires a separate compatibility/data assessment. Known rollback
edge cases are tracked in issues #21 and #23 until resolved.

Report the version tag, source commit, run URL, image digest, deployment and
backup verification, and any remaining limits. Creating or editing GitHub
Release notes is separate work unless the user requested it.
