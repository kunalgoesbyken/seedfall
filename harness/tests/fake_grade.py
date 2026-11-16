import json
import os
import sys
import time

HIDDEN = [987654321012345678, 123456789098765432]


def main(argv):
    args = dict(zip(argv[::2], argv[1::2]))
    log = os.environ.get("FAKE_LOG")
    if log:
        with open(log, "a", encoding="utf-8") as handle:
            handle.write(json.dumps({"argv": argv, "salt_in_env": "SEEDFALL_GYM_SALT" in os.environ, "key_in_env": "GEMINI_API_KEY" in os.environ}) + "\n")
    mode = os.environ.get("FAKE_MODE", "ok")
    if mode == "hang":
        time.sleep(120)
    if mode == "crash":
        print("grade: boom", file=sys.stderr)
        sys.exit(1)
    if "-seeds" in args:
        seeds = [int(s) for s in args["-seeds"].split(",")]
        per_seed = [
            {
                "seed": s,
                "passed": s % 2 == 0,
                "class": "assert",
                "reason": "lost write " + "x" * 1000,
                "acks": 7,
                "faults": ["f1", "f2"],
                "trace_tail": [f"line {i} " + "y" * 400 for i in range(40)] + ['event gym.ack from client#1: "k1"'],
            }
            for s in seeds
        ]
        print(json.dumps({"build_ok": True, "per_seed": per_seed, "guard": {"tripped": False}}))
        return
    print(json.dumps({"build_ok": True, "reward": 0.5, "per_seed": [{"seed": s, "passed": True} for s in HIDDEN]}))


if __name__ == "__main__":
    main(sys.argv[1:])
