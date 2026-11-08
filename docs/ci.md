# Running seedfall in CI

## GitHub Actions

```yaml
jobs:
  simulate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: kunalgoesbyken/seedfall@v0.2.0
        with:
          config: seedfall.json
          seeds: 1-500
          check: "true"
```

| Input | Default | Meaning |
|---|---|---|
| `config` | `seedfall.json` | Path to the world description |
| `seeds` | `1-200` | Seed or inclusive range |
| `check` | `false` | Run every seed twice and fail if the traces differ |
| `go-version` | `stable` | Go version for building seedfall and your nodes |
| `report` | `true` | Sweep every seed and upload an HTML triage report |
| `artifact-name` | `seedfall-report` | Name of the report artifact; make it unique per job in a matrix |

The job fails if any seed fails. The output goes to the job summary, and the triage report (see [reports.md](reports.md)) is uploaded as an artifact. Rotate the seed range over time, for example with the run number, to keep exploring new schedules while each failure stays reproducible.

## Machine-readable output

`seedfall run -json` sweeps every seed in the range and prints one JSON object per line as each seed finishes, then a summary object. Seeds finish in parallel, so lines are not in seed order.

```sh
seedfall run -c seedfall.always.json -seeds 1-100 -json | jq -c 'select(.type == "seed" and .passed == false)'
```

```json
{"type":"seed","seed":1,"passed":false,"finished":false,"failure":{"class":"assert","node":"client"},"reason":"client#1: acknowledged write lost: k96=v96, server now answers \"NONE\"","virtual_seconds":2.891820635,"faults":3}
```

| Field | Meaning |
|---|---|
| `type` | `seed` or `summary` |
| `seed`, `passed`, `finished` | The seed, whether it passed, and whether the workload finished before the time horizon |
| `failure` | `class`, plus `node` and `property` when known, the same key the triage report groups by |
| `reason` | The failure message |
| `virtual_seconds` | Simulated time the run covered |
| `faults` | Faults that happened during the run |

The summary has `passed`, `failed`, `unfinished`, `unmet_properties` and `properties`, the same coverage table `seedfall_run` returns over MCP, with a `problem` on each property that was never reached or never true. The exit code is 1 if any seed failed or any property is unmet. `-json` cannot be combined with `-report`.

## `go test`

```go
import "github.com/kunalgoesbyken/seedfall/seedfalltest"

func TestDurabilityUnderCrashes(t *testing.T) {
	seedfalltest.Sweep(t, "seedfall.json", "1-100")
}
```

A failure reports the seed, the reason, the trace tail and the replay command. A `Sometimes` property that is never satisfied also fails the test. `examples/kvlog/seedfall_test.go` is a working example.

## Speed

Runs are independent, so a sweep scales with cores: `seedfall run -j N` defaults to every CPU. Light servers take tens of milliseconds per seed; a 3-node hashicorp/raft cluster takes a few seconds. Each guest is compiled once per process and every seed reuses the compiled module, so per-seed cost is booting the nodes and running them. Compiled modules are also cached in `.seedfall/cache`, so add that directory to your CI cache to skip compilation; the first build of a large guest such as nats-server takes about a minute.
