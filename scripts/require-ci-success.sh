#!/usr/bin/env bash
# Waits for the CI workflow run of commit $SHA in $REPO and exits 0 only when
# it succeeded. A run that has not appeared yet gets five minutes to show up;
# a run still in progress is polled until it completes.
set -euo pipefail

: "${REPO:?set REPO to owner/name}"
: "${SHA:?set SHA to the commit to check}"
poll_seconds="${POLL_SECONDS:-60}"
missing=0

while true; do
  run=$(gh api "repos/$REPO/actions/workflows/ci.yml/runs?head_sha=$SHA&per_page=1" \
    --jq '.workflow_runs[0] | if . == null then "" else "\(.status) \(.conclusion // "none") \(.html_url)" end')
  if [ -z "$run" ]; then
    missing=$((missing + 1))
    if [ "$missing" -ge 10 ]; then
      echo "::error::No CI run exists for $SHA. Push the commit to a branch, let CI pass, then push the tag again."
      exit 1
    fi
    echo "No CI run for $SHA yet; waiting for it to be created."
    sleep "$((poll_seconds / 2 > 0 ? poll_seconds / 2 : 1))"
    continue
  fi
  read -r run_status conclusion url <<<"$run"
  if [ "$run_status" != "completed" ]; then
    echo "CI for $SHA is $run_status; waiting: $url"
    sleep "$poll_seconds"
    continue
  fi
  if [ "$conclusion" = "success" ]; then
    echo "CI passed for $SHA: $url"
    exit 0
  fi
  echo "::error::CI for $SHA ended with '$conclusion': $url. Make CI pass for this commit, then re-run this release."
  exit 1
done
