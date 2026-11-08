### seedfall verify: FAIL

Config `seedfall.json`, reproduce with `seedfall verify -seed 9`

| | Check | Result |
|---|---|---|
| pass | replay seed 9 | passes, twice, with identical host calls and traces |
| FAIL | fresh sweep, seeds 1-299 | seed 41 FAILS [bug]: server#2 crashed: panic |
| skip | determinism | an earlier check failed |
