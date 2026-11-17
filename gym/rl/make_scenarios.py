import argparse
import dataclasses
import json
import time

from rlenv import scenarios as S
from rlenv.reward import Reward

FILTER_SEEDS = list(range(90001, 90007))
MIN_ORACLE = 4 / 6


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--tree", required=True)
    ap.add_argument("--binary", required=True)
    ap.add_argument("--train", type=int, default=64)
    ap.add_argument("--test", type=int, default=32)
    ap.add_argument("--out", default="scenarios.json")
    ap.add_argument("--procs", type=int, default=2)
    ap.add_argument("--jobs", type=int, default=2)
    a = ap.parse_args()
    train, test = S.split(a.train, a.test)
    r = Reward(a.binary, a.tree, procs=a.procs, jobs=a.jobs, skip_doomed=False)
    rows = []
    t0 = time.time()
    for s in train + test:
        res = {name: r.score_ops(s, fn(s), FILTER_SEEDS) for name, fn in S.BASELINES.items() if name in ("oracle_rolling", "idle", "restart_all_together")}
        row = dataclasses.asdict(s)
        row["versions"] = list(s.versions)
        row["filter"] = {k: v["reward"] for k, v in res.items()}
        row["accepted"] = res["oracle_rolling"]["reward"] >= MIN_ORACLE and res["idle"]["reward"] == 0.0
        rows.append(row)
        print(s.id, row["filter"], row["accepted"], round(time.time() - t0), flush=True)
    r.close()
    json.dump({"filter_seeds": FILTER_SEEDS, "min_oracle": MIN_ORACLE, "scenarios": rows}, open(a.out, "w"), indent=1)
    print("accepted", sum(x["accepted"] for x in rows), "of", len(rows))


if __name__ == "__main__":
    main()
