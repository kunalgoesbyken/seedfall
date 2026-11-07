#!/usr/bin/env bash
set -u
root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$root/seedfall"
go build -o "$bin" "$root/cmd/seedfall" || exit 1

failures=0

expect() {
  local dir="$1" config="$2" seeds="$3" want="$4"
  local out status
  out="$(cd "$root/examples/$dir" && "$bin" run -c "$config" -seeds "$seeds" 2>&1)"
  status=$?
  local got="pass"
  [ $status -ne 0 ] && got="fail"
  local summary
  summary="$(printf '%s\n' "$out" | grep -m1 -E '^(ok|FAIL)')"
  if [ "$got" = "$want" ]; then
    printf 'PASS  %-8s %-24s want=%-4s  %s\n' "$dir" "$config" "$want" "$summary"
  else
    printf 'FAIL  %-8s %-24s want=%-4s  %s\n' "$dir" "$config" "$want" "$summary"
    failures=$((failures + 1))
  fi
}

expect_replay() {
  local dir="$1" config="$2" seed="$3" faults="$4" want="$5"
  local got="pass"
  (cd "$root/examples/$dir" && "$bin" replay -c "$config" -seed "$seed" -faults "$faults" >/dev/null 2>&1) || got="fail"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s want=%-4s  replay seed %s with %s: %s\n' "$verdict" "$dir" "$config" "$want" "$seed" "$faults" "$got"
}

deterministic() {
  local dir="$1" config="$2" seed="$3"
  local a b
  a="$(cd "$root/examples/$dir" && "$bin" replay -c "$config" -seed "$seed" 2>&1 | sed -E 's/[0-9.]+(ms|s) real//')"
  b="$(cd "$root/examples/$dir" && "$bin" replay -c "$config" -seed "$seed" 2>&1 | sed -E 's/[0-9.]+(ms|s) real//')"
  if [ "$a" = "$b" ]; then
    printf 'PASS  %-8s %-24s replay of seed %s is identical (%d lines)\n' "$dir" "$config" "$seed" "$(printf '%s\n' "$a" | wc -l)"
  else
    printf 'FAIL  %-8s %-24s replay of seed %s differs\n' "$dir" "$config" "$seed"
    failures=$((failures + 1))
  fi
}

expect kvlog  seedfall.json          1-50   fail
expect kvlog  seedfall.always.json   1-100  fail
expect kvlog  seedfall.fixed.json    1-300  pass
expect kvlog  seedfall.strict.json   1-200  pass
expect kvlog  seedfall.fsyncgate.json 1-50  fail
expect kvlog  seedfall.rewind.json   1-200  pass
expect raftkv seedfall.json          1-40   pass
expect raftkv seedfall.nofsync.json  1-120  fail
expect raftkv seedfall.lin.json      1-30   pass
expect raftkv nofsync.lin.json       1-120  fail
expect raftkv strict.lin.json        1-30   fail
expect raftkv tcp.lin.json           1-20   pass
expect raftkv tcp.nofsync.lin.json   1-150  fail
expect httpkv seedfall.json          1-40   pass
expect httpkv seedfall.nofsync.json  1-20   fail
expect wals   tidwall.json           1-50   fail
expect wals   tidwall.strict.json    1-50   fail
expect wals   rosedb.json            1-50   fail
expect wals   rosedb.strict.json     1-50   fail
expect buntdb   seedfall.json               1-100  pass
expect buntdb   seedfall.torn.json          1-100  pass
expect buntdb   seedfall.everysecond.json   1-20   fail
expect buntdb   seedfall.shrink.json        1-60   fail
expect buntdb   seedfall.shrink.strict.json 1-60   fail
expect etcdraft seedfall.json               1-30   pass
expect etcdraft seedfall.nofsync.json       1-60   fail
expect etcdraft seedfall.strict.json        1-30   pass
expect natsjs seedfall.json          1-20   fail
expect natsjs seedfall.always.json   1-60   pass
expect natsjs seedfall.strict.json   1-20   fail
expect_replay natsjs seedfall.always.json 40 metasum.faults.json fail
deterministic kvlog  seedfall.always.json 1
deterministic raftkv seedfall.lin.json    3
deterministic wals   rosedb.json             1

echo
[ $failures -eq 0 ] && echo "regression: all checks passed" || echo "regression: $failures check(s) failed"
exit $failures
