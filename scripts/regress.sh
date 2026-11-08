#!/usr/bin/env bash
set -u
root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$root/seedfall"
go build -o "$bin" "$root/cmd/seedfall" || exit 1

failures=0

expect() {
  local dir="$1" config="$2" seeds="$3" want="$4"
  local out status
  out="$(cd "$root/examples/$dir" && "$bin" run -c $config -seeds "$seeds" 2>&1)"
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

expect_command() {
  local name="$1"
  shift
  if "$bin" "$@" >/dev/null 2>&1; then
    printf 'PASS  %-8s\n' "$name"
  else
    printf 'FAIL  %-8s\n' "$name"
    failures=$((failures + 1))
  fi
}

expect_replay() {
  local dir="$1" config="$2" seed="$3" faults="$4" want="$5"
  local got="pass"
  (cd "$root/examples/$dir" && "$bin" replay -c $config -seed "$seed" -faults "$faults" >/dev/null 2>&1) || got="fail"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s want=%-4s  replay seed %s with %s: %s\n' "$verdict" "$dir" "$config" "$want" "$seed" "$faults" "$got"
}

expect_shrink() {
  local dir="$1" config="$2" seed="$3" want="$4"
  local got
  got="$(cd "$root/examples/$dir" && "$bin" shrink -c $config -seed "$seed" 2>&1 | sed -n -E 's/^ +to (.*)$/\1/p')"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s shrink seed %s: want %s, got %s\n' "$verdict" "$dir" "$config" "$seed" "$want" "$got"
}

expect_explore() {
  local dir="$1" config="$2" flags="$3" want="$4"
  local out status got="pass"
  out="$(cd "$root/examples/$dir" && rm -rf .seedfall/corpus && "$bin" explore -c $config $flags 2>&1)"
  status=$?
  [ $status -ne 0 ] && got="fail"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s want=%-4s  explore %s: %s\n' "$verdict" "$dir" "$config" "$want" "$flags" "$(printf '%s\n' "$out" | grep -m1 -E '^(stopped|FAIL)')"
}

expect_mutant() {
  local dir="$1" config="$2" seeds="$3" mutant="$4" want="$5"
  local got
  got="$(cd "$root/examples/$dir" && "$bin" mutate -c $config -seeds "$seeds" -only "$mutant" 2>&1 | awk -v m="$mutant" '$1 == m {print $2}')"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s mutant %s on seeds %s: want %s, got %s\n' "$verdict" "$dir" "$config" "$mutant" "$seeds" "$want" "$got"
}

expect_app_mutant() {
  local dir="$1" config="$2" seeds="$3" count="$4" site="$5" want="$6"
  local got
  got="$(cd "$root/examples/$dir" && "$bin" mutate -c $config -seeds "$seeds" -app -mutants "$count" 2>&1 | awk -v s="$site" '$1 == s {print $2}')"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s source mutant %s of %s on seeds %s: want %s, got %s\n' "$verdict" "$dir" "$config" "$site" "$count" "$seeds" "$want" "$got"
}

expect_pinned() {
  local dir="$1" config="$2" want="$3"
  local got
  got="$(cd "$root/examples/$dir" && "$bin" run -c $config -seeds 1 2>&1 | awk '/^regressions, replayed before the sweep/ {getline; print $1; exit}')"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s pinned regression: want %s, got %s\n' "$verdict" "$dir" "$config" "$want" "$got"
}

expect_timeline() {
  local dir="$1" config="$2" seed="$3" want="$4"
  local page="$root/.regress-timeline.html" got
  (cd "$root/examples/$dir" && "$bin" replay -c $config -seed "$seed" -html "$page" >/dev/null 2>&1)
  got="$(grep -o '"to_instance"' "$page" 2>/dev/null | wc -l | tr -d ' ')"
  rm -f "$page"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s timeline of seed %s: want %s messages, got %s\n' "$verdict" "$dir" "$config" "$seed" "$want" "$got"
}

expect_logs() {
  local dir="$1" config="$2" seed="$3" filter="$4" want="$5"
  local got
  got="$(cd "$root/examples/$dir" && "$bin" logs -c $config -seed "$seed" $filter 2>&1 >/dev/null | awk '{print $1}')"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s logs of seed %s %s: want %s lines, got %s\n' "$verdict" "$dir" "$config" "$seed" "$filter" "$want" "$got"
}

expect_grep() {
  local dir="$1" config="$2" seed="$3" search="$4" want="$5"
  local got
  got="$(cd "$root/examples/$dir" && "$bin" trace grep -c $config -seed "$seed" $search 2>&1 >/dev/null | awk '{print $1}')"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s trace grep of seed %s %s: want %s matches, got %s\n' "$verdict" "$dir" "$config" "$seed" "$search" "$want" "$got"
}

expect_edit() {
  local dir="$1" config="$2" seed="$3" faults="$4" edit="$5" want="$6"
  local edited="$root/.regress-edited.faults.json" got="pass"
  (cd "$root/examples/$dir" && "$bin" faults -c $config -seed "$seed" -faults "$faults" $edit -out "$edited" >/dev/null 2>&1 && "$bin" replay -c $config -seed "$seed" -faults "$edited" >/dev/null 2>&1) || got="fail"
  rm -f "$edited"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s replay seed %s with %s edited by %s: want %s, got %s\n' "$verdict" "$dir" "$config" "$seed" "$faults" "$edit" "$want" "$got"
}

expect_debug() {
  local dir="$1" config="$2" seed="$3" at="$4" command="$5" want="$6"
  local got
  got="$(cd "$root/examples/$dir" && printf '%s\nquit\n' "$command" | "$bin" debug -c $config -seed "$seed" -at "$at" 2>&1 | sed -n -E 's/.*paused at t=([^ ]+).*/\1/p' | tail -n 1)"
  local verdict="PASS"
  [ "$got" != "$want" ] && verdict="FAIL" && failures=$((failures + 1))
  printf '%s  %-8s %-24s debug seed %s from %s, %s: want %s, got %s\n' "$verdict" "$dir" "$config" "$seed" "$at" "$command" "$want" "$got"
}

deterministic() {
  local dir="$1" config="$2" seed="$3"
  local a b
  a="$(cd "$root/examples/$dir" && "$bin" replay -c $config -seed "$seed" 2>&1 | sed -E 's/[0-9hmsµ.]+ real//')"
  b="$(cd "$root/examples/$dir" && "$bin" replay -c $config -seed "$seed" 2>&1 | sed -E 's/[0-9hmsµ.]+ real//')"
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
expect kvlog  seedfall.readcorrupt.json 1-20 fail
expect kvlog  seedfall.checksum.json 1-80   pass
expect kvlog  seedfall.diskerrors.json 1-60 pass
expect kvlog  seedfall.lostwrite.json 1-20  fail
expect kvlog  seedfall.misdirected.json 1-20 fail
expect kvlog  seedfall.bitrot.json   1-20   fail
expect_app_mutant kvlog seedfall.fixed.json 1-10 4 server/main.go:157 killed
expect_app_mutant kvlog seedfall.fixed.json 1-10 4 server/main.go:197 SURVIVED
expect_replay kvlog seedfall.readcorrupt.json 1 readcorrupt.faults.json fail
expect_replay kvlog seedfall.checksum.json    1 readcorrupt.faults.json pass
expect_replay kvlog seedfall.always.json      1 torntail.faults.json fail
expect_replay kvlog seedfall.always.json      1 .seedfall/failures/seedfall.always-seed-1/faults.json fail
expect_pinned kvlog seedfall.fixed.json pass
expect_shrink kvlog seedfall.always.json      1 "2 faults, 991ms, 63 workload ops"
expect_timeline kvlog seedfall.always.json    1 538
expect_logs kvlog seedfall.always.json 1 "-node server" 8
expect_logs kvlog seedfall.always.json 1 "-net-trace -grep refused|reset" 92
expect_debug kvlog seedfall.always.json 1 1s "until fault crash" 1.705199634s
expect_grep kvlog seedfall.always.json 1 "-e crash" 2
expect_grep kvlog seedfall.always.json 1 "-net-trace -e refused|reset" 92
expect_edit kvlog seedfall.always.json 1 torntail.faults.json "-remove 1" pass
expect_edit kvlog seedfall.always.json 1 torntail.faults.json "-shift 1=-1" fail
deterministic kvlog "seedfall.always.json -net-trace" 1
expect raftkv seedfall.json          1-40   pass
expect raftkv seedfall.nofsync.json  1-120  fail
expect raftkv seedfall.lin.json      1-30   pass
expect raftkv nofsync.lin.json       1-120  fail
expect raftkv strict.lin.json        1-30   fail
expect raftkv tcp.lin.json           1-20   pass
expect raftkv pause.lin.json         1-60   pass
expect raftkv pause.localreads.lin.json 1-40 fail
expect raftkv tcp.nofsync.lin.json   1-150  fail
expect raftkv netfaults.json         1-20   pass
expect raftkv netfaults.localreads.lin.json 20 fail
expect_replay raftkv netfaults.localreads.lin.json 20 slowleader.faults.json fail
expect_timeline raftkv netfaults.localreads.lin.json 20 563
expect_logs raftkv netfaults.localreads.lin.json 20 "-net-trace -grep refused" 233
expect ledger seedfall.json          1-60   pass
expect ledger "seedfall.json -profile nofsync"  1-20   fail
expect_mutant ledger seedfall.json 1-20 net_dup_chunk killed
expect_mutant ledger seedfall.json 1-20 truncate_not_durable SURVIVED
expect httpkv seedfall.json          1-40   pass
expect httpkv "seedfall.json -profile nofsync"  1-20   fail
expect netcompat seedfall.json                  1-20   pass
expect netcompat "seedfall.json -profile sim"   1-20   pass
expect memberlist seedfall.json                 1-20   pass
expect memberlist "seedfall.json -profile strict" 1-5  fail
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
expect etcdraft "seedfall.json -profile nofsync"       1-60   fail
expect etcdraft "seedfall.json -profile strict"        1-30   pass
expect natsjs seedfall.json          1-20   fail
expect natsjs seedfall.always.json   1-60   pass
expect natsjs seedfall.strict.json   1-20   fail
expect_replay natsjs seedfall.always.json 40 metasum.faults.json fail
expect gostman seedfall.json          1-24   pass
expect gostman "seedfall.json -profile nofsync"  1-8    fail
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
expect metapage "seedfall.json -profile reorder" 1-30  fail
expect metapage "seedfall.json -profile fixed"   1-100 pass
expect_replay metapage "seedfall.json -profile reorder" 26 reorder.faults.json fail
expect mmapwal seedfall.json                 1-20 pass
expect mmapwal "seedfall.json -profile early" 1-5  fail
expect mmapwal "seedfall.json -profile fixed" 1-20 pass
expect_shrink mmapwal "seedfall.json -profile early" 2 "2 faults, 36ms, 3 workload ops"
expect symswap seedfall.json                 1-20 pass
expect symswap "seedfall.json -profile dirs" 1-20 fail
expect symswap "seedfall.json -profile fixed" 1-20 pass
expect natscluster seedfall.json            1-2  pass
expect natscluster "seedfall.json -profile pause"      1-4  pass
expect natscluster "seedfall.json -profile r1.nosync"  1-4  fail
expect_replay natscluster "seedfall.json -profile strict" 3 strict.faults.json fail
expect bboltkv  seedfall.json                  1-60  pass
expect bboltkv  seedfall.torn.json             1-60  pass
expect bboltkv  seedfall.strict.json           1-60  pass
expect bboltkv  seedfall.strict.nodirsync.json 1-10  fail
expect bboltkv  seedfall.nosync.json           1-10  fail
expect_replay bboltkv seedfall.strict.nodirsync.json 1 createdir.faults.json fail
expect pebblekv seedfall.json                  1-30  pass
expect pebblekv "seedfall.json -profile nosync"           1-10  fail
expect checkers queue.json         1-20  pass
expect checkers queue.racy.json    1-20  fail
expect checkers cas.json           1-20  pass
expect checkers cas.racy.json      1-20  fail
expect checkers counter.json       1-20  pass
expect checkers counter.racy.json  1-20  fail
expect checkers set.json           1-20  pass
expect checkers set.racy.json      1-20  fail
expect checkers append.json        1-20  pass
expect checkers append.racy.json   1-20  fail
expect checkers txn.json           1-20  pass
expect checkers txn.racy.json      1-20  fail
deterministic checkers txn.racy.json 1
expect interleave seedfall.json         1-50  pass
expect interleave interleave.json       1-5   fail
expect interleave interleave.lock.json  1-50  pass
deterministic interleave interleave.json 1
expect interleave journal.json                      1-5   fail
expect interleave "journal.json -profile none"      1-20  pass
expect interleave "journal.json -profile committed" 1-20  pass
expect_shrink interleave journal.json 1 "1 faults, 1ms"
deterministic interleave journal.json 1
expect busyloop seedfall.json                    1-3   fail
expect busyloop "seedfall.json -profile calls"   1-10  pass
expect busyloop "seedfall.json -profile recheck" 1-10  pass
expect busyloop "seedfall.json -profile spin"    1     fail
deterministic busyloop "seedfall.json -profile spin" 1
expect buggify seedfall.json         1-20  fail
expect buggify "seedfall.json -profile fixed"   1-20  pass
expect buggify "seedfall.json -profile off"     1-20  fail
expect_replay buggify seedfall.json 2 lost-reply.faults.json fail
expect_replay buggify "seedfall.json -profile fixed" 2 lost-reply.faults.json pass
deterministic buggify seedfall.json 2
expect explore seedfall.json 1-20 pass
expect_explore explore seedfall.json "-seeds 1-8 -rounds 30 -batch 8" fail
expect liveness seedfall.json            1-20  pass
expect liveness noredial.json            1-20  fail
expect liveness noredial.noliveness.json 1-20  fail
expect liveness nodedupe.json            1-20  fail
expect_replay liveness noredial.json 1 stuck.faults.json fail
expect_replay liveness seedfall.json 1 stuck.faults.json pass
expect_replay liveness nodedupe.json 2 client-crash.faults.json fail
expect_replay liveness seedfall.json 2 client-crash.faults.json pass
deterministic liveness seedfall.json 1
deterministic ledger seedfall.json 3
expect upgrade seedfall.json                      1-100 pass
expect upgrade "seedfall.json -profile nomigrate" 1-5   fail
expect upgrade "seedfall.json -profile inplace"   1-30  fail
expect_replay upgrade "seedfall.json -profile inplace" 21 tornmigration.faults.json fail
expect_replay upgrade seedfall.json 21 tornmigration.faults.json pass
deterministic upgrade "seedfall.json -profile inplace" 21
expect aikv configs/haiku-1.reorder.json 1     fail
expect aikv configs/opus-2.harsh.json    99    fail
expect aikv configs/sonnet-3.reorder.json 1-10 pass
deterministic kvlog  seedfall.always.json 1
deterministic kvlog  seedfall.diskerrors.json 3
deterministic metapage "seedfall.json -profile reorder" 16
deterministic raftkv seedfall.lin.json    3
deterministic raftkv netfaults.json       3
deterministic wals   rosedb.json             1
expect kvlog "seedfall.fixed.json -timeout 1h" 1-20 pass
expect_command version --version
expect_command help help run
expect_command doctor doctor

echo
[ $failures -eq 0 ] && echo "regression: all checks passed" || echo "regression: $failures check(s) failed"
exit $failures
