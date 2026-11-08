### seedfall verify: FAIL

Config `seedfall.json`, reproduce with `seedfall verify -seed 9 -mutate`

| | Check | Result |
|---|---|---|
| pass | replay seed 9 | passes, twice, with identical host calls and traces |
| pass | fresh sweep, seeds 1-299 | 299 of 299 pass: 95% confidence that a failure with rate >= 0.997% would have shown up (historical rate 1.67%) |
| pass | determinism, seeds 1-20 | every seed ran twice with identical host calls and traces |
| FAIL | mutant dirsync_noop | SURVIVED: the checks no longer notice this guarantee disappearing |
| FAIL | mutant server/main.go:197 | SURVIVED: the checks still pass with cmd[0] == "PUT" -> cmd[0] != "PUT" |
| pass | mutant server/main.go:157 | killed by seed 1: err != nil -> err == nil |
