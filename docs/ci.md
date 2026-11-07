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

The job fails on the lowest failing seed. The run's output, including the replay command, goes to the job summary. Rotate the seed range over time, for example with the run number, to keep exploring new schedules while each failure stays reproducible.

## `go test`

```go
import "github.com/kunalgoesbyken/seedfall/seedfalltest"

func TestDurabilityUnderCrashes(t *testing.T) {
	seedfalltest.Sweep(t, "seedfall.json", "1-100")
}
```

A failure reports the seed, the reason, the trace tail and the replay command. A `Sometimes` property that is never satisfied also fails the test. `examples/kvlog/seedfall_test.go` is a working example.

## Speed

Runs are independent, so a sweep scales with cores: `seedfall run -j N` defaults to every CPU. Light servers take tens of milliseconds per seed; a 3-node hashicorp/raft cluster takes a few seconds. Compiled modules are cached in `.seedfall/cache`, so add that directory to your CI cache to skip compilation.
