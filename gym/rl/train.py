import argparse
import inspect
import json
import os
import random
import statistics
import sys
import time
from importlib.metadata import version

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from rlenv import metrics as M
from rlenv import prompt as P
from rlenv import scenarios as S
from rlenv import setup_env
from rlenv.reward import Reward

TRAIN_SEEDS = range(1000, 50000)
TEST_SEEDS = list(range(50001, 50017))
VAL_SEEDS = list(range(60001, 60009))
LIBRARIES = ("torch", "transformers", "trl", "peft", "accelerate", "datasets")


def load_scenarios(path):
    rows = [r for r in json.load(open(path))["scenarios"] if r["accepted"]]
    fields = S.Scenario.__dataclass_fields__
    make = lambda r: S.Scenario(**{k: (tuple(v) if k == "versions" else v) for k, v in r.items() if k in fields})
    return [make(r) for r in rows if r["id"].startswith("train")], [make(r) for r in rows if r["id"].startswith("test")]


def split_val(train, every):
    ordered = sorted(train, key=lambda s: (s.n, len(s.stale), s.id))
    ids = {s.id for s in ordered[every - 1 :: every]}
    return [s for s in train if s.id not in ids], [s for s in train if s.id in ids]


def prompt_text(tok, s):
    return tok.apply_chat_template(P.messages(s), tokenize=False, add_generation_prompt=True, enable_thinking=False)


def sample(model, tok, prompts, n, a):
    import torch

    tok.padding_side = "left"
    model.eval()
    outs = []
    per_batch = max(1, a.gen_batch // n)
    for i in range(0, len(prompts), per_batch):
        chunk = prompts[i : i + per_batch]
        enc = tok(chunk, return_tensors="pt", padding=True).to(model.device)
        with torch.no_grad():
            g = model.generate(**enc, do_sample=True, temperature=a.temperature, top_p=a.top_p, top_k=0, min_p=a.min_p or None, max_new_tokens=a.max_new_tokens, num_return_sequences=n, pad_token_id=tok.pad_token_id, use_cache=True)
        texts = tok.batch_decode(g[:, enc.input_ids.shape[1] :], skip_special_tokens=True)
        for j in range(len(chunk)):
            outs.append(texts[j * n : (j + 1) * n])
    return outs


def evaluate(tag, model, tok, reward, scenarios, n_samples, seeds, a):
    prompts = [prompt_text(tok, s) for s in scenarios]
    t = time.time()
    outs = sample(model, tok, prompts, n_samples, a)
    gen_s = time.time() - t
    items = [(s, text, tuple(seeds)) for s, texts in zip(scenarios, outs) for text in texts]
    t = time.time()
    res = reward.score_many(items)
    rew_s = time.time() - t
    model.train()
    with open(os.path.join(a.out, f"samples_{tag}.jsonl"), "w") as f:
        for (s, text, _), r in zip(items, res):
            f.write(json.dumps({"scenario": s.id, "reward": r["reward"], "status": r["status"], "error": r.get("error"), "reasons": r.get("reasons"), "text": text}) + "\n")
    summary = M.summarize(res)
    summary.update({"tag": tag, "distinct_per_scenario": M.distinct_per_scenario(items), "scenarios": len(scenarios), "samples_per_scenario": n_samples, "seeds_per_sample": len(seeds), "seeds": list(seeds), "gen_seconds": gen_s, "reward_seconds": rew_s, "per_scenario": M.per_scenario(items, res)})
    json.dump(summary, open(os.path.join(a.out, f"eval_{tag}.json"), "w"), indent=1)
    print(tag, {k: v for k, v in summary.items() if k != "per_scenario"}, flush=True)
    return summary


def baselines(reward, scenarios, seeds, a):
    reward.skip_doomed = False
    out = {}
    for name, fn in S.BASELINES.items():
        t = time.time()
        rs = reward.score_many_ops([(s, fn(s), tuple(seeds)) for s in scenarios])
        out[name] = {"mean_reward": statistics.fmean(r["reward"] for r in rs), "mean_replay": statistics.fmean(r["replay"] for r in rs), "pass_all_rate": sum(r["reward"] == 1.0 for r in rs) / len(rs), "n": len(rs), "seeds": len(seeds), "seconds": time.time() - t, "per_scenario": {s.id: r["reward"] for s, r in zip(scenarios, rs)}}
        print("baseline", name, out[name]["mean_reward"], flush=True)
    reward.skip_doomed = True
    json.dump(out, open(os.path.join(a.out, "baselines.json"), "w"), indent=1)
    return out


class TrainReward:
    __name__ = "seedfall_verify"

    def __init__(self, reward, by_id, k, log_path):
        self.reward, self.by_id, self.k, self.log_path, self.calls = reward, by_id, k, log_path, 0
        self.samples_path = os.path.join(os.path.dirname(log_path), "train_samples.jsonl")
        open(self.samples_path, "w").close()

    def __call__(self, prompts, completions, scenario_id, **kw):
        self.calls += 1
        seeds = {}
        items = []
        for sid, text in zip(scenario_id, completions):
            if sid not in seeds:
                seeds[sid] = tuple(sorted(random.Random(f"{sid}/{self.calls}").sample(TRAIN_SEEDS, self.k)))
            items.append((self.by_id[sid], text, seeds[sid]))
        before = dict(self.reward.stats)
        t = time.time()
        res = self.reward.score_many(items)
        dt = time.time() - t
        row = M.summarize(res)
        row.update({"call": self.calls, "seconds": dt, "distinct_per_scenario": M.distinct_per_scenario(items), "episodes": self.reward.stats["episodes"] - before["episodes"], "doomed": self.reward.stats["doomed"] - before["doomed"], "cached": self.reward.stats["cached"] - before["cached"], "unique_scenarios": len(seeds)})
        M.jsonl_append(self.log_path, row)
        M.jsonl_append(self.samples_path, {"call": self.calls, "samples": [{"scenario": it[0].id, "reward": r["reward"], "text": it[1]} for it, r in zip(items, res)]})
        print("reward call", self.calls, round(row["mean_reward"], 3), "touch", round(row["touch_rate"], 2), "distinct", round(row["distinct_per_scenario"], 1), "eps", row["episodes"], round(dt, 1), "s", flush=True)
        return [r["reward"] for r in res]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--tree", required=True)
    ap.add_argument("--scenarios", required=True)
    ap.add_argument("--out", default="/kaggle/working/results")
    ap.add_argument("--work", default="/tmp/work")
    ap.add_argument("--model", default="Qwen/Qwen3-1.7B")
    ap.add_argument("--procs", type=int, default=2)
    ap.add_argument("--jobs", type=int, default=2)
    ap.add_argument("--eval-scenarios", type=int, default=28)
    ap.add_argument("--eval-samples", type=int, default=8)
    ap.add_argument("--eval-seeds", type=int, default=16)
    ap.add_argument("--val-every-nth", type=int, default=5)
    ap.add_argument("--val-samples", type=int, default=4)
    ap.add_argument("--val-seeds", type=int, default=8)
    ap.add_argument("--val-steps", type=int, default=25)
    ap.add_argument("--steps", type=int, default=200)
    ap.add_argument("--prompts-per-step", type=int, default=4)
    ap.add_argument("--generations", type=int, default=12)
    ap.add_argument("--train-seeds", type=int, default=8)
    ap.add_argument("--lr", type=float, default=2e-5)
    ap.add_argument("--beta", type=float, default=0.005)
    ap.add_argument("--epsilon-high", type=float, default=0.28)
    ap.add_argument("--entropy-coef", type=float, default=0.005)
    ap.add_argument("--entropy-target", type=float, default=0.02)
    ap.add_argument("--entropy-coef-max", type=float, default=0.03)
    ap.add_argument("--temperature", type=float, default=1.0)
    ap.add_argument("--top-p", type=float, default=1.0)
    ap.add_argument("--min-p", type=float, default=0.05)
    ap.add_argument("--max-new-tokens", type=int, default=640)
    ap.add_argument("--gen-batch", type=int, default=32)
    ap.add_argument("--lora-rank", type=int, default=32)
    ap.add_argument("--budget-hours", type=float, default=9.0)
    ap.add_argument("--skip-base", action="store_true")
    ap.add_argument("--base-only", action="store_true")
    ap.add_argument("--no-baselines", action="store_true")
    ap.add_argument("--baselines-only", action="store_true")
    a = ap.parse_args()
    random.seed(0)
    os.makedirs(a.out, exist_ok=True)
    t_start = time.time()

    setup_env.install_go()
    os.makedirs(a.work, exist_ok=True)
    tree, binary = a.tree, setup_env.build_seedfall(a.tree, os.path.join(a.work, "seedfall"))
    print("built seedfall", flush=True)
    train_sc, test_sc = load_scenarios(a.scenarios)
    test_sc = test_sc[: a.eval_scenarios]
    train_sc, val_sc = split_val(train_sc, a.val_every_nth)
    reward = Reward(binary, tree, procs=a.procs, jobs=a.jobs)
    reward.score_many_ops = lambda items: list(reward.pool.map(lambda it: reward.score_ops(*it), items))
    print("scenarios train", len(train_sc), "val", len(val_sc), "test", len(test_sc), flush=True)

    if a.baselines_only:
        results = {"config": vars(a), "cpus": os.cpu_count()}
        results["baselines"] = baselines(reward, test_sc, TEST_SEEDS[: a.eval_seeds], a)
        results["reward_stats"] = reward.stats
        json.dump(results, open(os.path.join(a.out, "results.json"), "w"), indent=1)
        reward.close()
        return

    import torch
    from peft import LoraConfig, get_peft_model, get_peft_model_state_dict, set_peft_model_state_dict
    from transformers import AutoModelForCausalLM, AutoTokenizer, set_seed

    set_seed(0)
    tok = AutoTokenizer.from_pretrained(a.model)
    model = AutoModelForCausalLM.from_pretrained(a.model, dtype=torch.float16, attn_implementation="sdpa").to("cuda")
    model.gradient_checkpointing_enable(gradient_checkpointing_kwargs={"use_reentrant": False})
    model.enable_input_require_grads()
    model = get_peft_model(model, LoraConfig(r=a.lora_rank, lora_alpha=2 * a.lora_rank, target_modules=["q_proj", "k_proj", "v_proj", "o_proj", "gate_proj", "up_proj", "down_proj"], task_type="CAUSAL_LM"))
    seeds = TEST_SEEDS[: a.eval_seeds]
    results = {"config": vars(a), "gpu": torch.cuda.get_device_name(0), "versions": {p: version(p) for p in LIBRARIES}, "split": {"train": [s.id for s in train_sc], "val": [s.id for s in val_sc], "test": [s.id for s in test_sc]}, "val": []}

    def save():
        path = os.path.join(a.out, "results.json")
        with open(path + ".tmp", "w") as f:
            json.dump(results, f, indent=1)
        os.replace(path + ".tmp", path)

    from datasets import Dataset
    from transformers import TrainerCallback
    from trl import GRPOConfig, GRPOTrainer

    rng = random.Random(0)
    rows = []
    while len(rows) < a.steps * a.prompts_per_step:
        order = list(train_sc)
        rng.shuffle(order)
        rows += [{"prompt": prompt_text(tok, s), "scenario_id": s.id} for s in order]
    ds = Dataset.from_list(rows[: a.steps * a.prompts_per_step])
    by_id = {s.id: s for s in train_sc}
    log_path = os.path.join(a.out, "train_rewards.jsonl")
    open(log_path, "w").close()
    fn = TrainReward(reward, by_id, a.train_seeds, log_path)

    cfg_args = dict(
        output_dir=os.path.join(a.work, "trainer"),
        learning_rate=a.lr,
        lr_scheduler_type="constant_with_warmup",
        warmup_steps=5,
        per_device_train_batch_size=a.generations,
        gradient_accumulation_steps=a.prompts_per_step,
        num_generations=a.generations,
        max_completion_length=a.max_new_tokens,
        temperature=a.temperature,
        top_p=a.top_p,
        top_k=0,
        min_p=a.min_p or None,
        beta=a.beta,
        loss_type="dapo",
        scale_rewards="none",
        epsilon_high=a.epsilon_high,
        use_adaptive_entropy=a.entropy_coef > 0,
        entropy_coef=a.entropy_coef,
        entropy_target=a.entropy_target,
        entropy_coef_delta=a.entropy_coef / 5,
        entropy_coef_max=a.entropy_coef_max,
        max_steps=a.steps,
        logging_steps=1,
        save_strategy="no",
        report_to="none",
        fp16=True,
        bf16=False,
        remove_unused_columns=False,
        seed=0,
    )
    accepted = set(inspect.signature(GRPOConfig.__init__).parameters)
    dropped = sorted(set(cfg_args) - accepted)
    if dropped:
        raise SystemExit(f"GRPOConfig does not accept {dropped}")
    cfg = GRPOConfig(**{k: v for k, v in cfg_args.items() if k in accepted})

    snapshot = lambda: {k: v.detach().to("cpu", copy=True) for k, v in get_peft_model_state_dict(model).items()}
    best = {"step": 0, "reward": None, "state": None}

    def validate_now(step):
        torch.cuda.empty_cache()
        try:
            validate_step(step)
        except Exception as e:
            results.setdefault("val_errors", []).append({"step": step, "error": repr(e)[:300]})
            save()
            print("validation failed at step", step, repr(e), flush=True)
        finally:
            model.train()
            torch.cuda.empty_cache()

    def validate_step(step):
        row = evaluate(f"val_{step:04d}", model, tok, reward, val_sc, a.val_samples, VAL_SEEDS[: a.val_seeds], a)
        results["val"].append({"step": step, **{k: row[k] for k in ("mean_reward", "mean_replay", "pass_all_rate", "touch_rate", "valid_rate", "distinct_per_scenario")}})
        if best["reward"] is None or row["mean_reward"] > best["reward"]:
            best.update(step=step, reward=row["mean_reward"], state=snapshot())
            model.save_pretrained(os.path.join(a.out, "adapter_best"))
        results["best_step"] = best["step"]
        save()

    class Budget(TrainerCallback):
        def on_step_end(self, args, state, control, **kw):
            if time.time() - t_start > a.budget_hours * 3600:
                control.should_training_stop = True
            if state.global_step % a.val_steps == 0 or control.should_training_stop or state.global_step >= args.max_steps:
                validate_now(state.global_step)

        def on_log(self, args, state, control, logs=None, **kw):
            M.jsonl_append(os.path.join(a.out, "trainer_log.jsonl"), {"step": state.global_step, **(logs or {})})

    trainer = GRPOTrainer(model=model, reward_funcs=[fn], args=cfg, train_dataset=ds, processing_class=tok, callbacks=[Budget()])
    if not a.no_baselines:
        results["baselines"] = baselines(reward, test_sc, seeds, a)
        save()
    if not a.skip_base:
        results["base_test"] = evaluate("base_test", model, tok, reward, test_sc, a.eval_samples, seeds, a)
        save()
    if a.base_only:
        results["reward_stats"] = reward.stats
        save()
        reward.close()
        return

    validate_now(0)
    t = time.time()
    try:
        trainer.train()
    except Exception as e:
        results["train_error"] = repr(e)[:500]
        print("training stopped by", repr(e), flush=True)
        torch.cuda.empty_cache()
    results["train_seconds"] = time.time() - t
    results["train_steps_done"] = trainer.state.global_step
    model.save_pretrained(os.path.join(a.out, "adapter_final"))
    save()

    results["final_test"] = evaluate("final_test", model, tok, reward, test_sc, a.eval_samples, seeds, a)
    save()
    if best["step"] != trainer.state.global_step:
        set_peft_model_state_dict(model, best["state"])
        results["best_test"] = evaluate("best_test", model, tok, reward, test_sc, a.eval_samples, seeds, a)
    else:
        results["best_test"] = results["final_test"]
    results["reward_stats"] = reward.stats
    save()
    reward.close()


if __name__ == "__main__":
    main()
