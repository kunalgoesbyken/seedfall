import json
import os
import sys

from scale_specs import SYSTEMS, TASKS

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")
MANIFEST = os.path.join(ROOT, "tasks.json")
FIRST_GENERATION = 10


def main():
    with open(MANIFEST, newline="") as f:
        manifest = json.loads(f.read())
    kept = manifest["tasks"][:FIRST_GENERATION]
    used = {t["system"] for t in kept} | {v.get("system", t["system"]) for t in kept for v in t.get("variants", [])} | {t["system"] for t in TASKS}
    for name, system in SYSTEMS.items():
        manifest["systems"].setdefault(name, system)
    manifest["systems"] = {name: system for name, system in manifest["systems"].items() if name in used}
    existing = {t["id"] for t in kept}
    only = set(sys.argv[1:])
    ids = [t["id"] for t in TASKS]
    if len(set(ids)) != len(ids) or existing & set(ids):
        sys.exit("duplicate task ids")
    previous = {t["id"]: t for t in manifest["tasks"][FIRST_GENERATION:]}
    new = [t for t in TASKS if not only or t["id"] in only]
    for t in new:
        previous[t["id"]] = t
    ordered = [previous[i] for i in ids if i in previous]
    manifest["tasks"] = kept + ordered
    with open(MANIFEST, "w", newline="\n") as f:
        f.write(json.dumps(manifest, indent=2))
    print("manifest has %d tasks" % len(manifest["tasks"]))


if __name__ == "__main__":
    main()
