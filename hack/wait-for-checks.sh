#!/usr/bin/env bash
# Waits until every workflow in GATED_WORKFLOWS has succeeded on commit SHA,
# and fails if one failed, never started or did not finish in time. The
# release and latest workflows run it before they publish anything.
#
# Environment:
#   GH_TOKEN, GH_REPO   for the gh CLI (a token that can read Actions runs)
#   SHA                 the commit whose push-event runs are checked
#   GATED_WORKFLOWS     workflow files that must have succeeded, one per line
#   GRACE_MINUTES       a workflow whose run doesn't appear within this many
#                       minutes is treated as failed (it was probably skipped
#                       or never triggered); keep it well under the job timeout
#   WAIT_MINUTES        give up waiting after this many minutes
#   POLL_SECONDS        pause between checks
#   ACTION, RUN_NAME    for the error messages: what is not being done
#                       ("releasing") and which workflow's run to re-run
#                       ("Release")
set -euo pipefail
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
start=$(date +%s)
while true; do
  # One entry per run; a re-run reuses the run and bumps its
  # attempt, so this always reflects the latest attempt. Runs
  # are matched by workflow file, not display name.
  runs=$(gh api --paginate \
    "repos/${GH_REPO}/actions/runs?head_sha=${SHA}&event=push&per_page=100" \
    --jq '.workflow_runs[]' | jq -s '.')
  elapsed=$(( $(date +%s) - start ))
  failed=0 pending=0
  table=""
  while IFS= read -r wf; do
    [ -n "${wf}" ] || continue
    row=$(jq -r --arg wf "${wf}" '
      [.[] | select((.path | split("@")[0]) == $wf)]
      | sort_by(.created_at) | last // empty
      | [.name, .status, (.conclusion // ""), .html_url, (.run_attempt | tostring)]
      | @tsv' <<<"${runs}")
    if [ -z "${row}" ]; then
      if [ "${elapsed}" -ge $(( GRACE_MINUTES * 60 )) ]; then
        failed=1
        table+="| ${wf} | no run started within ${GRACE_MINUTES} minutes |"$'\n'
      else
        pending=1
        table+="| ${wf} | not started yet |"$'\n'
      fi
      continue
    fi
    IFS=$'\t' read -r name status conclusion url attempt <<<"${row}"
    if [ "${status}" != "completed" ]; then
      pending=1
      table+="| [${name}](${url}) | ${status} (attempt ${attempt}) |"$'\n'
    elif [ "${conclusion}" != "success" ]; then
      failed=1
      table+="| [${name}](${url}) | **${conclusion}** (attempt ${attempt}) |"$'\n'
    else
      table+="| [${name}](${url}) | success (attempt ${attempt}) |"$'\n'
    fi
  done <<<"${GATED_WORKFLOWS}"
  if [ "${failed}" -eq 1 ] || [ "${pending}" -eq 0 ] \
    || [ "${elapsed}" -ge $(( WAIT_MINUTES * 60 )) ]; then
    break
  fi
  echo "Waiting (${elapsed}s elapsed)..."
  sleep "${POLL_SECONDS}"
done
{
  echo "### Checks on ${SHA}"
  echo
  echo "| Workflow | Result |"
  echo "| --- | --- |"
  printf '%s' "${table}"
} >> "${summary}"
printf '%s' "${table}"
if [ "${failed}" -eq 1 ]; then
  echo "::error::A required workflow did not succeed on ${SHA}; not ${ACTION}. Fix or re-run it, then re-run the failed jobs of this ${RUN_NAME} run."
  exit 1
fi
if [ "${pending}" -eq 1 ]; then
  echo "::error::Timed out after ${WAIT_MINUTES} minutes waiting for the workflows on ${SHA}; not ${ACTION}. Re-run the failed jobs of this ${RUN_NAME} run once they have finished."
  exit 1
fi
echo "All required workflows succeeded on ${SHA}."

