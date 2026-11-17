import os
import subprocess
import sys

ARGS = ["--no-baselines", "--eval-samples", "4", "--prompts-per-step", "3", "--steps", "120", "--val-steps", "20", "--budget-hours", "8.0"]


def sh(cmd, **kw):
    print("+", cmd, flush=True)
    return subprocess.run(cmd, shell=True, **kw)


sh("find /kaggle/input -maxdepth 3 | head -30")
found = [os.path.join(r, f) for r, _, fs in os.walk("/kaggle/input") for f in fs if f == "bundle.tar.gz"]
os.makedirs("/tmp/bundle", exist_ok=True)
if found:
    sh(f"tar -xzf {found[0]} -C /tmp/bundle", check=True)
else:
    root = [r for r, ds, _ in os.walk("/kaggle/input") if "rl" in ds and "tree" in ds][0]
    sh(f"cp -r {root}/rl {root}/tree /tmp/bundle/", check=True)
sh("pip list 2>/dev/null | grep -iE '^(torch|transformers|trl|peft|accelerate|datasets|bitsandbytes|unsloth|vllm) '")
sh("pip uninstall -y torchao 2>&1 | tail -1")
sh("pip install -q trl==1.18.0 peft==0.21.2 transformers==5.20.2 accelerate==1.15.0 datasets==4.8.5 2>&1 | tail -3")
sh("pip list 2>/dev/null | grep -iE '^(torch|transformers|trl|peft|accelerate|datasets|bitsandbytes|unsloth|vllm) '")
sh("nproc; nvidia-smi --query-gpu=name,memory.used,memory.total --format=csv")
cmd = [sys.executable, "/tmp/bundle/rl/train.py", "--tree", "/tmp/bundle/tree", "--scenarios", "/tmp/bundle/rl/scenarios.json", "--out", "/kaggle/working/results"] + ARGS
code = sh("CUDA_VISIBLE_DEVICES=0 " + " ".join(cmd)).returncode
sh("ls -la /kaggle/working/results")
sys.exit(code)
