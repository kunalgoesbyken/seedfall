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
    printf '%s\n' "$out" | head -n 4 | sed 's/^/      /'
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
  a="$(cd "$root/examples/$dir" && "$bin" replay -c "$config" -seed "$seed" 2>&1 | sed -E 's/[0-9hmsµ.]+ real//')"
  b="$(cd "$root/examples/$dir" && "$bin" replay -c "$config" -seed "$seed" 2>&1 | sed -E 's/[0-9hmsµ.]+ real//')"
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
expect raftkv pause.lin.json         1-60   pass
expect raftkv pause.localreads.lin.json 1-40 fail
expect raftkv tcp.nofsync.lin.json   1-150  fail
expect ledger seedfall.json          1-60   pass
expect ledger seedfall.nofsync.json  1-20   fail
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
expect gostman seedfall.json          1-24   pass
expect gostman seedfall.nofsync.json  1-8    fail
expect nutsdb   seedfall.json               1-20  fail
expect nutsdb   seedfall.fsyncmeta.json     1-40  pass
expect nutsdb   seedfall.torn.json          1-20  fail
expect nutsdb   seedfall.strict.json        1-30  fail
expect nutsdb   seedfall.fsyncgate.json     1-20  fail
expect nutsdb   seedfall.nosync.json        1-20  fail
expect bitcask  seedfall.json               1-40  pass
expect bitcask  seedfall.nosync.json        1-20  fail
expect bitcask  seedfall.torn.json          1-20  fail
expect bitcask  seedfall.recovery.json      1-20  fail
expect bitcask  seedfall.strict.json        1-30  fail
expect bitcask  seedfall.fsyncgate.json     1-20  fail
expect diskv    seedfall.json               1-60  pass
expect diskv    seedfall.nosync.json        1-30  fail
expect diskv    seedfall.inplace.json       1-30  fail
expect diskv    seedfall.strict.json        1-20  fail
expect jmsraft seedfall.json          1-8   fail
expect jmsraft seedfall.strict.json   1-8   fail
expect jmsraft seedfall.eio.json      1-8   fail
expect jmsraft clock.json             9-16  fail
expect jmsraft pause.json             1-8   pass
expect jmsraft pause.lease.json       1-8   pass
expect jmsraft localreads.json        1-4   fail
expect jmsraft grpc.json              1-12  pass
expect_replay jmsraft seedfall.json 4  statefile.faults.json       fail
expect_replay jmsraft seedfall.json 16 statefile.crash.faults.json fail
expect_replay jmsraft seedfall.json 7  tornlog.faults.json         fail
expect_replay jmsraft seedfall.json 16 tmpdir.faults.json          fail
expect_replay jmsraft clock.json    13 snapshotorder.faults.json   fail
expect metapage seedfall.json         1-100 pass
expect metapage seedfall.reorder.json 1-30  fail
expect metapage seedfall.fixed.json   1-100 pass
expect_replay metapage seedfall.reorder.json 16 reorder.faults.json fail
expect natscluster seedfall.json            1-2  pass
expect natscluster seedfall.pause.json      1-4  pass
expect natscluster seedfall.r1.nosync.json  1-4  fail
expect_replay natscluster seedfall.strict.json 3 strict.faults.json fail
expect bboltkv  seedfall.json                  1-60  pass
expect bboltkv  seedfall.torn.json             1-60  pass
expect bboltkv  seedfall.strict.json           1-60  pass
expect bboltkv  seedfall.strict.nodirsync.json 1-10  fail
expect bboltkv  seedfall.nosync.json           1-10  fail
expect_replay bboltkv seedfall.strict.nodirsync.json 1 createdir.faults.json fail
expect pebblekv seedfall.json                  1-30  pass
expect pebblekv seedfall.nosync.json           1-10  fail
expect interleave seedfall.json         1-50  pass
expect interleave interleave.json       1-5   fail
expect interleave interleave.lock.json  1-50  pass
deterministic interleave interleave.json 1
deterministic kvlog  seedfall.always.json 1
deterministic metapage seedfall.reorder.json 16
deterministic raftkv seedfall.lin.json    3
deterministic wals   rosedb.json             1

echo
[ $failures -eq 0 ] && echo "regression: all checks passed" || echo "regression: $failures check(s) failed"
exit $failures
