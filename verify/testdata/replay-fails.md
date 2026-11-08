### seedfall verify: FAIL

Config `seedfall.json`, seedfall `v0.2.0`, reproduce with `seedfall verify -seed 9`

| | Check | Result |
|---|---|---|
| FAIL | replay seed 9 | seed 9 FAILS [bug]: client#1: acknowledged write lost \| k3 |
| skip | fresh sweep | an earlier check failed |
| skip | determinism | an earlier check failed |
