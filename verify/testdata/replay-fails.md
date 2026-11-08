### seedfall verify: FAIL

Config `seedfall.json`, reproduce with `seedfall verify -seed 9`

| | Check | Result |
|---|---|---|
| FAIL | replay seed 9 | seed 9 FAILS [bug]: client#1: acknowledged write lost \| k3 |
| skip | fresh sweep | an earlier check failed |
| skip | determinism | an earlier check failed |
