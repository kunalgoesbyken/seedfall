### seedfall verify: PASS

Config `seedfall.json`, reproduce with `seedfall verify -seed 9`

| | Check | Result |
|---|---|---|
| pass | replay seed 9 | passes, twice, with identical traces |
| pass | fresh sweep, seeds 1-299 | 299 of 299 pass: 95% confidence that a failure with rate >= 0.997% would have shown up (historical rate 1.67%) |
| pass | determinism, seeds 1-20 | every seed ran twice with identical traces |
| pass | mutant fsync_noop | killed by seed 4 |
| pass | mutant dirsync_noop | killed by seed 7 |
