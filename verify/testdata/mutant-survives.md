### seedfall verify: FAIL

Config `seedfall.json`, reproduce with `seedfall verify -seed 9 -mutate`

| | Check | Result |
|---|---|---|
| pass | replay seed 9 | passes, twice, with identical traces |
| pass | fresh sweep, seeds 1-299 | 299 of 299 pass: 95% confidence that a failure with rate >= 0.997% would have shown up (historical rate 1.67%) |
| pass | determinism, seeds 1-20 | every seed ran twice with identical traces |
| FAIL | mutant dirsync_noop | SURVIVED: the checks no longer notice this durability guarantee disappearing |
