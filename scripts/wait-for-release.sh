#!/usr/bin/env bash
# Waits until the Release workflow run for tag $TAG at commit $SHA in $REPO
# has published the GitHub release, and exits 1 if that run ends without
# publishing it.
set -euo pipefail

: "${REPO:?set REPO to owner/name}"
: "${TAG:?set TAG to the release tag}"
: "${SHA:?set SHA to the tagged commit}"
poll_seconds="${POLL_SECONDS:-60}"

while true; do
  # Read the run before the release: Release publishes before its run
  # completes, so a run already completed without a published release failed.
  run=$(gh api -X GET "repos/$REPO/actions/workflows/release.yml/runs" \
    -f event=push -f branch="$TAG" -f head_sha="$SHA" -f per_page=1 \
    --jq '.workflow_runs[0] | if . == null then "" else "\(.status) \(.conclusion // "none") \(.html_url)" end')
  draft=$(gh release view "$TAG" --repo "$REPO" --json isDraft --jq .isDraft 2>/dev/null || true)
  if [ "$draft" = "false" ]; then
    echo "Release $TAG is published."
    exit 0
  fi
  if [ -z "$run" ]; then
    echo "::error::No Release run exists for $TAG at $SHA."
    exit 1
  fi
  read -r run_status conclusion url <<<"$run"
  if [ "$run_status" = "completed" ]; then
    echo "::error::Release for $TAG ended with '$conclusion' without publishing it: $url. Re-run it, then re-run this job."
    exit 1
  fi
  echo "Release for $TAG is $run_status; waiting: $url"
  sleep "$poll_seconds"
done
