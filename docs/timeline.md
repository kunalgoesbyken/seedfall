# Timelines, logs and time-travel debugging

A failing seed replays exactly, so you can look at it as many times and in as many ways as you like. seedfall gives you four views of one run:

| View | Command | What it shows |
|---|---|---|
| Timeline | `seedfall replay -html timeline.html`, and `timeline.html` in every [failure artifact](reports.md#failure-artifacts) folder | A space-time diagram: one lane per node, every connection and message as an arrow, fault intervals shaded, recorded operations as bars, properties and events as markers, next to a filterable trace |
| Sequence diagram | `seedfall replay -html timeline.mmd`, and `timeline.mmd` in every artifact folder | The same run as a Mermaid `sequenceDiagram`, for an issue or a pull request |
| Logs | `seedfall logs -seed N -node server -grep re -from 1s -to 2s` | The trace filtered by node, pattern and virtual time, with app output interleaved with engine events and slog JSON parsed into fields |
| Debugger | `seedfall debug -seed N -at 1.7s` | A prompt paused at a moment of the run: node states, the live disk, in-flight messages and active faults, then step, run until a pattern, or branch into a different future |

`seedfall trace grep` finds a pattern and shows the lines of every node around each match, and `seedfall trace diff` compares two traces and shows where they first part ways. Logs, trace search and the debugger are also MCP tools, and the timeline is in every artifact folder the MCP tools name.

## The trace

Every run already traces node starts, crashes, faults and checks, plus every line each node writes to stdout or stderr, stamped with the virtual time it was written:

```
    0.000s  start server#1
    0.000s  server#1| serving 0 keys on :7000
    0.292s  fault partition [client] | rest, heals at 0.627s
    0.757s  fault crash server#1 during disk op 56, restart at 1.121s
```

`server#2` is the second process started on node `server`, after its first crash. With `-net-trace` on `run`, `replay`, `logs` or `debug`, the trace also has every connection, message sent and message read:

```
    1.120s  net client#1 dials server:7000, refused
    1.121s  start server#2
    1.121s  server#2| serving 26 keys on :7000
    1.130s  net client#1 dials server:7000 conn 57, arrives 1.132s
    1.130s  net client#1 -> server#2 conn 57: 12 bytes, arrives 1.132s
    1.132s  net server#2 accepts conn 57 from client#1
    1.132s  net server#2 <- client#1 conn 57: 12 bytes sent at 1.130s
    1.132s  net server#2 -> client#1 conn 57: 3 bytes, arrives 1.137s
```

A message sent while a partition or a clog holds the link says `held by a partition or clog`, and a duplicate from the `net_dup_chunk` mutant says `a duplicate`. UDP datagrams appear as `udp <socket>` lines, and one that never arrived says `lost` and why: a partition or clog, no socket bound to the address, or a `udp_drop` fault. `-net-trace` adds lines to the trace only: it does not change the schedule, the determinism hash or the result, so a seed fails the same way with and without it. A failure replayed with `-net-trace` writes those lines into its artifact `trace.txt` as well.

## Timeline

```sh
seedfall replay -c seedfall.always.json -seed 1 -html timeline.html
```

`timeline.html` is one self-contained file with no network fetches, so it opens from a CI artifact or an email attachment. Every failure seedfall reports also gets one in its artifact folder, next to `trace.txt`, and the [triage report](reports.md) embeds it under each finding.

The diagram has one column per node with virtual time flowing down:

| Mark | Meaning |
|---|---|
| Green line | A process of the node is alive, from its start to its crash or exit, labelled with its instance (`server#2`) |
| Arrow | A connection (`connect`, purple) or a message (data, blue; EOF, yellow; reset, red) from the sender at the time it was sent to the receiver at the time it was first read. A dashed arrow was never read: it was dropped by a crash, a reset or the end of the run. Hover for the connection, size and times |
| Shaded box | A fault interval over the nodes it hits: a crash until the restart, a pause, a slowdown, a clog, a clock jump, or a partition over every lane |
| Bar | An operation recorded with `sim.Put`, `sim.Get`, `sim.Invoke` or `sim.Txn`, from invocation to completion, green when ok, red when failed, yellow when unknown. Concurrent operations of one node sit side by side |
| Dot | A `sim.Event`, the first time a `Sometimes` or `Reachable` property is true, or the first time an `Always` or `Eventually` property is checked |
| Red line | The failure, with its reason |

Rows are the distinct moments something happened, labelled with their virtual time, so a run with long idle stretches stays compact; the spacing is not proportional to time.

The trace explorer beside it lists every trace line. Above both are a node toggle per node (plus `engine` for lines that belong to no node), toggles for lives, messages, operations, faults and marks, a `from` and `to` window in Go duration syntax (`1.5s`, `200ms`), and a `grep` box that filters trace lines with a JavaScript regular expression. Clicking a trace line scrolls the diagram to its moment.

## Sequence diagram

```sh
seedfall replay -c seedfall.always.json -seed 1 -html timeline.mmd
```

A path ending in `.mmd` writes Mermaid instead of HTML:

```
sequenceDiagram
  participant p0 as server
  participant p1 as client
  Note over p0: 0.000s start server#35;1
  p1->>p0: 0.000s conn 2 connect, read 0.003s
  p1->>p0: 0.000s conn 2 10 bytes, read 0.003s
  p0->>p1: 0.003s conn 2 3 bytes, read 0.007s
  p1-->>p0: 0.007s conn 2 eof, read 0.009s
```

It has the node lives, messages (`->>` read, `-->>` an EOF or reset, `-x` never read or a lost datagram), faults, events, properties and the failure, in time order. Recorded operations are only in the HTML timeline. GitHub renders a ` ```mermaid ` block in issues and pull requests; a run with thousands of messages makes a diagram too tall to read there, so cut it to the moments around the failure first.

## Logs

```sh
seedfall logs -c seedfall.always.json -seed 1 -node server
```

```
    0.000s  start server#1
    0.000s  server#1| serving 0 keys on :7000
    0.757s  fault crash server#1 during disk op 56, restart at 1.121s
    1.121s  start server#2
    1.121s  server#2| serving 26 keys on :7000
    1.705s  fault crash server#2, restart at 1.943s
    1.943s  start server#3
    1.943s  server#3| serving 60 keys on :7000
8 of 12 trace lines; seed 1: FAIL client#1: acknowledged write lost: k96=v96, server now answers "NONE"
```

| Flag | Meaning |
|---|---|
| `-seed`, `-faults` | The run to replay, as for `replay` |
| `-node` | Only lines that belong to these nodes, comma separated: their output, starts, crashes, faults aimed at them and, with `-net-trace`, the messages they send and read. Lines about the whole run, such as a partition or the end of the workload, belong to no node and are left out |
| `-grep` | Only lines matching this Go regular expression |
| `-from`, `-to` | Only lines in this virtual time window, for example `-from 1.5s -to 2s` |
| `-json` | One JSON object per line instead of text |
| `-net-trace` | Add connections and messages to the trace first |

The summary line goes to stderr, so `seedfall logs ... > server.log` keeps only the trace. With `-json`, each line has `at`, `at_ns`, `node`, `instance`, the full trace `line` and, for a node's own output, `output`. When that output is a JSON object, such as a line from `slog.NewJSONHandler`, it is parsed into `fields`:

```json
{"at":"100.000013ms","at_ns":100000013,"node":"app","instance":"app#1","line":"    0.100s  app#1   | {\"time\":\"2026-01-01T00:00:00.100000012Z\",\"level\":\"INFO\",\"msg\":\"put\",\"key\":1}","output":"{\"time\":\"2026-01-01T00:00:00.100000012Z\",\"level\":\"INFO\",\"msg\":\"put\",\"key\":1}","fields":{"key":1,"level":"INFO","msg":"put","time":"2026-01-01T00:00:00.100000012Z"}}
```

slog's `time` field is the guest's wall clock, which seedfall simulates from 2026-01-01 (and skews or jumps under clock faults); `at` is the virtual time the line was written. Other slog handlers' text output comes through as `output` without `fields`.

## Searching the trace

`logs -grep` keeps only the matching lines. To see what happened around each match, across every node, use `seedfall trace grep`:

```sh
seedfall trace grep -c seedfall.always.json -seed 1 -e crash -context 1
```

```
     4     0.292s  fault partition [client] | rest, heals at 0.627s
>    5     0.757s  fault crash server#1 during disk op 56, restart at 1.121s
     6     1.121s  start server#2
     7     1.121s  server#2| serving 26 keys on :7000
>    8     1.705s  fault crash server#2, restart at 1.943s
     9     1.943s  start server#3
2 matches of 12 trace lines, 2 shown; seed 1: FAIL client#1: acknowledged write lost: k96=v96, server now answers "NONE"
```

Matches are marked `>`, each line carries its number in the run's trace (the same as the artifact's `trace.txt` for that run) and its virtual time, windows that touch or overlap are merged, and separate windows are divided by `--`. `-e` is a Go regular expression. `-node`, `-from` and `-to` narrow which lines can match, as for `logs`, while the context lines come from every node. `-context` sets the lines before and after each match (3 by default), `-limit` the most matches shown (200), `-net-trace` adds connections and messages first, and `-json` prints the windows as one JSON object with the same line fields as `logs -json` plus `n` and `match`.

## Debugger

```sh
seedfall debug -c seedfall.always.json -seed 1 -at 1.7s
```

`debug` replays the seed and stops before the first scheduler step after `-at` (the same moment `seedfall inspect -at` snapshots), then reads commands:

```
paused at t=1.696068226s before scheduler step 379
  server       server#2     blocked
  client       client#1     runnable at 1.702612857s
(seedfall 1.696068226s) until fault crash
    1.705s  fault crash server#2, restart at 1.943s
paused at t=1.705199634s before scheduler step 382
  server                    down until 1.942588993s
  client       client#1     runnable at 1.706780887s
(seedfall 1.705199634s) net
  conn 125  server#2 -> client#1: eof, sent at 1.705199634s, arrives at 1.706780887s
(seedfall 1.705199634s) stat server data/kv.log
data/kv.log on server: 527 bytes, 527 durable, directory entry durable: true
```

| Command | Effect |
|---|---|
| `where` | Virtual time, the next scheduler step, and each node's state: `runnable` at a time, `blocked` with no timer or message due, `paused` or `down` until a time, `exited` |
| `log [n]` | The last `n` trace lines, 20 by default |
| `grep <regex>` | Every trace line so far that matches |
| `ls [node]` | Every node's disk, or one node's, in the [inspect](inspect.md) format: live and durable sizes, what a crash loses, torn writes and damage |
| `stat <node> <path>` | One file's size, durable size, the byte ranges a crash loses and its damage |
| `cat <node> <path>` | The file's live content, what the node reads now, as text or a hex dump (`inspect -cat` shows the durable content instead) |
| `net` | Every message still in flight with its sender, receiver, size, send and arrival time, and connections not yet accepted |
| `faults` | Active faults and the next scheduled one |
| `step [n]` | Run `n` scheduler steps, 1 by default |
| `until <regex>` | Run until a new trace line matches, then stop right after it |
| `to <duration>` | Run until the next step is after this virtual time |
| `branch [key]` | Re-randomize everything after now with this key, 1 by default: the scheduler, network and disk randomness, guest randomness and the faults still to come, as [`seedfall cause`](cause.md) does. One branch per session |
| `quit` | End the session |

Commands after `step`, `until` and `to` print the trace lines that ran and the new position. Nothing runs between commands, and the run is the same one `replay` produces, so the state you see is the state the failing run had. Once the run ends, inspection commands still work on the final state.

The debugger stops between scheduler steps, not inside a node's code: it shows what a crash at that moment would see, not a goroutine's stack. For source-level stepping, rebuild the node natively and reproduce with the faults from `faults.json`.

## Comparing traces

```sh
seedfall trace diff .seedfall/failures/seedfall.always-seed-1 after-fix.txt
```

Each side is a trace file (an artifact's `trace.txt`, or `seedfall logs` output saved to a file) or an artifact folder, which means its `trace.txt`. It prints the line where the traces first differ with the matching lines before it (`-context 3`), and exits 1, or says they are identical and exits 0. `-context` goes before the two paths. Here `a.txt` is the trace of seed 1 and `b.txt` the trace of the same seed under its shrunk schedule, both saved with `seedfall logs`:

```
$ seedfall trace diff a.txt b.txt
the traces diverge at line 4 (a.txt has 12 lines, b.txt has 12)

      0.000s  start server#1
      0.000s  start client#1
      0.000s  server#1| serving 0 keys on :7000
-     0.292s  fault partition [client] | rest, heals at 0.627s
+     0.000s  client#1: sim.Budget capped at 63 of 200 operations
```

Use it after shrinking to see where the shrunk schedule leaves the original run, after a fix to see the first moment the run behaves differently, or on two machines to find where a nondeterministic run splits (`run -check` does that for you within one machine).

## Over MCP

`seedfall_logs` takes `config`, `seed` and optionally `faults`, `node`, `grep`, `from`, `to`, `net_trace` and `limit` (200 by default), and returns the matching lines with the same fields as `logs -json`, how many matched and how many the trace has. `seedfall_trace_search` takes `config`, `seed`, `pattern` and optionally `faults`, `context`, `node`, `from`, `to`, `net_trace` and `limit`, and returns the same windows as `trace grep -json`. `seedfall_debug` takes `config`, `seed`, optionally `faults`, `at`, `net_trace` and a `commands` list run in order (`["until fault crash", "net", "ls server"]`), and returns each command's output and the final state. Failures from `seedfall_run`, `seedfall_replay` and `seedfall_shrink` name the artifact folder, which holds `timeline.html` and `timeline.mmd`. See [agents.md](agents.md#tools).
