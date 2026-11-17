import json
import statistics
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt

BASELINES = (("idle", "idle"), ("restart_all_together", "restart all together"), ("stale_at_deadline", "stale at the deadline"), ("restart_stale_together", "stale together"), ("oracle_rolling", "oracle rolling restart"))


def smooth(xs, w):
    return [statistics.fmean(xs[max(0, i - w + 1) : i + 1]) for i in range(len(xs))]


def main(results_dir, out):
    rows = [json.loads(l) for l in open(f"{results_dir}/train_rewards.jsonl")]
    res = json.load(open(f"{results_dir}/results.json"))
    base = json.load(open(f"{results_dir}/baselines_16seeds.json"))
    steps = [r["call"] for r in rows]
    fig, ax = plt.subplots(1, 2, figsize=(11, 4))
    ax[0].plot(steps, [r["mean_reward"] for r in rows], alpha=0.3, label="training reward per step")
    ax[0].plot(steps, smooth([r["mean_reward"] for r in rows], 10), label="moving average (10)")
    ax[0].plot([v["step"] for v in res["val"]], [v["mean_reward"] for v in res["val"]], marker="o", label="validation reward")
    ax[0].axhline(base["oracle_rolling"]["mean_reward"], color="green", linestyle=":", label="oracle on test")
    ax[0].set_xlabel("GRPO step")
    ax[0].set_ylim(0, 1.02)
    ax[0].legend(fontsize=8)
    names = [label for _, label in BASELINES] + ["base model", "GRPO model"]
    vals = [base[k]["mean_reward"] for k, _ in BASELINES] + [res["base_test"]["mean_reward"], res["final_test"]["mean_reward"]]
    ax[1].barh(names, vals, color=["#999"] * len(BASELINES) + ["#c55", "#27a"])
    ax[1].set_xlim(0, 1)
    test = res["final_test"]
    ax[1].set_xlabel(f"{test['scenarios']} held-out scenarios, {test['seeds_per_sample']} hidden seeds: mean reward")
    plt.tight_layout()
    plt.savefig(out, dpi=140)


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
