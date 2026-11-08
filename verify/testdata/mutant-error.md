### seedfall verify: FAIL

Config `seedfall.json`, seedfall `v0.2.0`, reproduce with `seedfall verify -seed 9 -app-mutants 3`

| | Check | Result |
|---|---|---|
| pass | replay seed 9 | passes, twice, with identical host calls and traces |
| pass | fresh sweep, seeds 1-299 | 299 of 299 pass: 95% confidence that a failure with rate >= 0.997% would have shown up (historical rate 1.67%) |
| pass | determinism, seeds 1-20 | every seed ran twice with identical host calls and traces |
| FAIL | source mutants | go list ./server: exit status 1 |
