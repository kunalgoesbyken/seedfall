#!/usr/bin/env bash
set -uo pipefail

bin="$RUNNER_TEMP/seedfall"
out="$RUNNER_TEMP/seedfall-report"
log="$RUNNER_TEMP/seedfall.txt"
rm -rf "$out"
mkdir -p "$out"

fail_with() {
  local body="$out/error.md"
  printf '### seedfall: ERROR\n\n%s\n' "$1" >"$body"
  {
    echo "status=error"
    echo "body=$body"
  } >>"$GITHUB_OUTPUT"
  cat "$body" >>"$GITHUB_STEP_SUMMARY"
}

summarize_log() {
  {
    echo '<details><summary>seedfall output</summary>'
    echo
    echo '````'
    tail -n 60 "$log"
    echo '````'
    echo
    echo '</details>'
  } >>"$GITHUB_STEP_SUMMARY"
}

refuse() {
  echo "::error title=seedfall::$1"
  fail_with "$1"
  exit 1
}

report_path=""
produced=""
case "$COMMAND" in
  run) ;;
  verify) report_path="$out/proof.md" produced="$report_path" ;;
  mutate) report_path="$out/mutants.md" produced="$report_path" ;;
  *) refuse "the command input must be run, verify or mutate" ;;
esac
if [ "$COMMAND" != run ]; then
  [ "$CHECK" != true ] || refuse "the check input only applies to command run; verify checks determinism itself and mutate has no such check"
  [ "$ROTATE" != true ] || refuse "the rotate-seeds input only applies to command run"
elif [ "$COMMENT" = true ] && [ "$REPORT" != true ]; then
  refuse "the comment input needs report: true"
fi

args=("$COMMAND" -c "$CONFIG" -profile "$PROFILE")
[ -z "$SEEDS" ] || args+=(-seeds "$SEEDS")
if [ "$COMMAND" = run ]; then
  args+=(-check="$CHECK")
  [ "$ROTATE" != true ] || args+=(-rotate "$((GITHUB_RUN_NUMBER - 1))")
  if [ "$REPORT" = true ]; then
    report_path="$out/report.html"
    produced="$out/report.json"
    args+=(-report "$report_path")
  fi
else
  args+=(-md "$report_path")
fi
read -r -a extra <<<"$EXTRA_ARGS" || true

"$bin" "${args[@]}" ${extra[@]+"${extra[@]}"} 2>&1 | tee "$log"
code="${PIPESTATUS[0]}"

if [ "$code" -ne 0 ] && [ -n "$produced" ] && [ ! -f "$produced" ]; then
  fail_with "seedfall could not finish; its last lines of output are in the job summary."
  summarize_log
  exit "$code"
fi

status=passed
[ "$code" -eq 0 ] || status=failed
body=""
if [ "$COMMAND" != run ]; then
  body="$report_path"
elif [ -f "$produced" ]; then
  body="$out/summary.md"
  "$bin" report github -report "$report_path" -md "$body"
fi
{
  echo "status=$status"
  echo "body=$body"
  [ -z "$report_path" ] || echo "report-path=$report_path"
} >>"$GITHUB_OUTPUT"

{
  [ -z "$body" ] || cat "$body"
  echo
} >>"$GITHUB_STEP_SUMMARY"
summarize_log

exit "$code"
