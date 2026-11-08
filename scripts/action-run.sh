#!/usr/bin/env bash
set -uo pipefail

bin="$RUNNER_TEMP/seedfall"
out="$RUNNER_TEMP/seedfall-report"
log="$RUNNER_TEMP/seedfall.txt"
rm -rf "$out"
mkdir -p "$out"

refuse() {
  echo "::error title=seedfall::$1"
  echo "status=error" >>"$GITHUB_OUTPUT"
  printf '### seedfall\n\n%s\n' "$1" >>"$GITHUB_STEP_SUMMARY"
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

status=passed
if [ "$code" -ne 0 ]; then
  status=failed
  [ -z "$produced" ] || [ -f "$produced" ] || status=error
fi
{
  echo "status=$status"
  [ ! -f "$report_path" ] || echo "report-path=$report_path"
} >>"$GITHUB_OUTPUT"

[ ! -f "$out/report.json" ] || "$bin" report github -report "$out/report.html"

body=""
[ "$COMMAND" = run ] || [ ! -f "$report_path" ] || body="$report_path"
if [ "$status" = error ]; then
  body="$out/error.md"
  printf '### seedfall: ERROR\n\nseedfall could not finish; see the job log.\n' >"$body"
fi
echo "body=$body" >>"$GITHUB_OUTPUT"

{
  [ -z "$body" ] || cat "$body"
  if [ -z "$body" ] || [ "$status" = error ]; then
    echo '### seedfall'
    echo '```'
    tail -n 60 "$log"
    echo '```'
  fi
} >>"$GITHUB_STEP_SUMMARY"

exit "$code"
