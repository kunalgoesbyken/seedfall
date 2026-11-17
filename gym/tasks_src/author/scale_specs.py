SYSTEMS = {}
TASKS = []


def add(task, system, bug_class, difficulty, symptom, ack, base, config=None, n=None, cheat=None):
    patches = {
        "buggy": ack + base + [task + "-bug.patch"],
        "fixed": [task + "-fix.patch"],
        "cheat": [cheat or task + "-cheat.patch"],
    }
    spec = {"id": task, "system": system, "bug_class": bug_class, "difficulty": difficulty, "visible_seed": 1, "symptom": symptom, "patches": patches}
    if config:
        spec["config"] = config
    if n:
        spec["n"] = n
    TASKS.append(spec)


KVLOG = dict(system="kvlog", ack=["kvlog-client-ack.patch"], base=["kvlog-full.patch"])

add("kvlog-no-dir-sync", bug_class="missing-dir-fsync-on-create", difficulty="medium", symptom="TODO", config={"faults": {"strict_dirs": True, "crash_per_sec": 1}}, **KVLOG)
add("kvlog-sync-wrong-handle", bug_class="fsync-wrong-target", difficulty="easy", symptom="TODO", **KVLOG)
add("kvlog-truncate-off-by-one", bug_class="recovery-off-by-one", difficulty="hard", symptom="TODO", config={"faults": {"crash_per_sec": 2, "crash_on_io": 0.04}}, **KVLOG)
add("kvlog-write-error-ignored", bug_class="write-error-ignored", difficulty="easy", symptom="TODO", config={"faults": {"write_eio": 0.03, "crash_per_sec": 1.5}}, **KVLOG)
add("kvlog-no-reread", bug_class="transient-read-error-destroys-data", difficulty="hard", symptom="TODO", config={"faults": {"read_corrupt": 0.7, "crash_per_sec": 1.5}}, **KVLOG)
add("kvlog-sync-before-write", bug_class="fsync-before-write-order", difficulty="easy", symptom="TODO", **KVLOG)
add("kvlog-rollback-truncates-log", bug_class="error-path-destroys-data", difficulty="medium", symptom="TODO", config={"faults": {"fsync_eio": 0.02, "crash_per_sec": 1}, "supervise": True}, **KVLOG)
add("kvlog-fsync-error-swallowed", bug_class="fsync-error-ignored", difficulty="medium", symptom="TODO", config={"faults": {"fsync_eio": 0.03, "crash_per_sec": 1}, "supervise": True, "duration": "20s"}, **KVLOG)

SYMSWAP = dict(system="symswap", ack=["kvlog-client-ack.patch"], base=["symswap-full.patch"])
SYMSWAP_FAULTS = {"duration": "120s", "workload": {"args": ["-writes", "200"]}, "faults": {"disk_latency": ["1ms", "10ms"], "crash_per_sec": 3, "restart_after": ["10ms", "100ms"]}}

add("symswap-gen-unsynced", bug_class="rename-before-data-fsync", difficulty="medium", symptom="TODO", config=SYMSWAP_FAULTS, **SYMSWAP)
add("symswap-no-publish-sync", bug_class="missing-dir-fsync-after-rename", difficulty="medium", symptom="TODO", config=SYMSWAP_FAULTS, **SYMSWAP)
add("symswap-gc-before-publish", bug_class="gc-before-pointer-durable", difficulty="hard", symptom="TODO", config=SYMSWAP_FAULTS, **SYMSWAP)

LEDGER = dict(system="ledger", ack=["ledger-client-ack.patch"], base=["ledger-full.patch"], cheat=None)
add("ledger-torn-tail-kept", bug_class="torn-tail-recovery", difficulty="medium", symptom="TODO", config={"faults": {"crash_per_sec": 1, "restart_after": ["1ms", "5ms"], "crash_on_io": 0.1}}, **LEDGER)
add("ledger-sync-before-write", bug_class="fsync-before-write-order", difficulty="easy", symptom="TODO", **LEDGER)

HTTPKV = dict(system="httpkv", ack=["httpkv-client-ack.patch"], base=["httpkv-full.patch"])
add("httpkv-no-dir-sync", bug_class="missing-dir-fsync-on-create", difficulty="medium", symptom="TODO", config={"faults": {"strict_dirs": True, "crash_per_sec": 1}}, **HTTPKV)
add("httpkv-torn-tail-kept", bug_class="torn-tail-recovery", difficulty="medium", symptom="TODO", config={"workload": {"args": ["-writes", "300"]}, "faults": {"crash_per_sec": 3, "crash_on_io": 0.15}}, **HTTPKV)

SYSTEMS["metapage"] = {
    "trees": {"server": "examples/metapage/server", "client": "examples/kvlog/client"},
    "editable": ["server/"],
    "exploits": ["forge-kvlog", "refuse-kvlog"],
    "config": {
        "nodes": [{"name": "server", "pkg": "./server", "args": ["-dir", "data"]}],
        "workload": {"name": "client", "pkg": "./client", "args": ["-writes", "200"]},
        "duration": "60s",
        "latency": ["1ms", "5ms"],
        "faults": {"crash_per_sec": 1, "restart_after": ["10ms", "500ms"], "torn_writes": True, "crash_on_io": 0.02, "reorder_writes": True},
    },
}
METAPAGE = dict(system="metapage", ack=["kvlog-client-ack.patch"], base=["metapage-full.patch"])
add("metapage-single-meta-slot", bug_class="in-place-metadata-update", difficulty="hard", symptom="TODO", **METAPAGE)
add("metapage-meta-unsynced", bug_class="ack-before-fsync", difficulty="easy", symptom="TODO", **METAPAGE)

LIVENESS = dict(system="liveness", ack=["liveness-client-ack.patch"], base=["liveness-dedupe-on.patch", "liveness-full.patch"])
add("liveness-log-unsynced", bug_class="ack-before-fsync", difficulty="easy", symptom="TODO", **LIVENESS)
add("liveness-applied-not-recovered", bug_class="dedupe-table-lost-on-restart", difficulty="hard", symptom="TODO", config={"faults": {"crash_on_io": 0.1, "crash_per_sec": 1}}, **LIVENESS)
add("liveness-backup-error-ignored", bug_class="replication-error-ignored", difficulty="medium", symptom="TODO", **LIVENESS)


def aikv_system(impl):
    return {
        "trees": {"impl/" + impl: "examples/aikv/impl/" + impl, "server": "examples/aikv/server", "client": "examples/aikv/client"},
        "editable": ["impl/%s/" % impl],
        "module_from": "examples/aikv",
        "config": {
            "nodes": [{"name": "server", "pkg": "./server", "args": ["-impl", impl]}],
            "workload": {"name": "client", "pkg": "./client", "args": ["-ops", "5000", "-size", "8192", "-gap", "5ms"]},
            "duration": "120s",
            "supervise": True,
            "latency": ["1ms", "5ms"],
            "faults": {"crash_per_sec": 0.5, "restart_after": ["10ms", "500ms"], "torn_writes": True, "crash_on_io": 0.005, "reorder_writes": True, "fs_model": "page_independent", "strict_dirs": True},
        },
    }


AIKV_RUN = {"workload": {"args": ["-ops", "1500", "-size", "8192", "-gap", "5ms"]}, "duration": "60s"}


def aikv(task, impl, bug_class, difficulty, faults=None, full=False):
    SYSTEMS.setdefault("aikv-" + impl, aikv_system(impl))
    config = {} if full else {**AIKV_RUN}
    if faults:
        config["faults"] = faults
    add(task, "aikv-" + impl, bug_class, difficulty, "TODO", ["aikv-client-ack.patch"], ["aikv-%s-server.patch" % impl], config=config, n=16 if full else 32)


aikv("aikv-sonnet-1-base-unsynced", "sonnet-1", "rename-before-data-fsync", "hard", {"crash_per_sec": 1.5}, full=True)
aikv("aikv-opus-1-fsync-error-ignored", "opus-1", "fsync-error-ignored", "medium", {"fsync_eio": 0.03})
aikv("aikv-sonnet-3-compact-unsynced", "sonnet-3", "rename-before-data-fsync", "hard", {"crash_per_sec": 1.5}, full=True)


RAFT_EXPLOITS = {"raftkv": ["forge-raftkv"]}


def raft_system(example, trees, workload, nodes, config):
    cfg = {"nodes": nodes, "workload": {"name": "client", "pkg": "./" + workload}, "duration": "120s", "latency": ["1ms", "8ms"]}
    cfg.update(config)
    system = {"trees": {t: "examples/%s/%s" % (example, t) for t in trees}, "editable": ["node/"], "module_from": "examples/" + example, "config": cfg}
    if example in RAFT_EXPLOITS:
        system["exploits"] = RAFT_EXPLOITS[example]
    return system


def raft_nodes(args=None):
    return [{"name": n, "pkg": "./node", **({"args": args(i)} if args else {})} for i, n in enumerate(["n1", "n2", "n3"], 1)]


RAFT_FAULTS = {"crash_per_sec": 0.15, "restart_after": ["50ms", "1s"], "partition_per_sec": 0.2, "partition_for": ["100ms", "2s"], "torn_writes": True, "crash_on_io": 0.002}
SYSTEMS["etcdraft"] = raft_system("etcdraft", ["node", "linclient"], "linclient", raft_nodes(lambda i: ["-id", str(i)]), {"faults": RAFT_FAULTS})
SYSTEMS["jmsraft"] = raft_system("jmsraft", ["node", "linclient"], "linclient", raft_nodes(), {"net_overlay": True, "supervise": True, "watchdog": "180s", "faults": {"pause_per_sec": 1.0, "pause_for": ["200ms", "2s"], "clock_jump_per_sec": 0.1, "clock_jump_max": "5s"}})
SYSTEMS["raftkv"] = raft_system("raftkv", ["node", "client"], "client", raft_nodes(), {"faults": RAFT_FAULTS})

ETCD = dict(system="etcdraft", ack=["etcdraft-client-ack.patch"], base=[])
add("etcdraft-wal-unsynced", bug_class="ack-before-fsync", difficulty="medium", symptom="TODO", n=16, config={"faults": {"crash_per_sec": 0.5}}, **ETCD)
add("etcdraft-snapshot-data-unsynced", bug_class="rename-before-data-fsync", difficulty="hard", symptom="TODO", n=16, config={"faults": {"crash_per_sec": 0.5}}, **ETCD)
add("raftkv-log-unsynced", bug_class="ack-before-fsync", difficulty="hard", symptom="TODO", n=16, config={"faults": {"crash_per_sec": 0.6, "crash_on_io": 0.005}}, **dict(system="raftkv", ack=["raftkv-client-ack.patch"], base=[]))

add("liveness-apply-before-replicate", bug_class="apply-before-replicate", difficulty="medium", symptom="TODO", **LIVENESS)
add("metapage-append-offset-off-by-one", bug_class="append-offset-off-by-one", difficulty="medium", symptom="TODO", **METAPAGE)
add("symswap-fsync-wrong-dir", bug_class="fsync-wrong-target", difficulty="medium", symptom="TODO", config=SYMSWAP_FAULTS, **SYMSWAP)


add("httpkv-sync-before-write", bug_class="fsync-before-write-order", difficulty="easy", symptom="TODO", **HTTPKV)

from scale_symptoms import SYMPTOMS

VISIBLE_SEEDS = {
    "kvlog-no-dir-sync": 2,
    "symswap-gc-before-publish": 4,
    "httpkv-no-dir-sync": 2,
    "aikv-opus-1-fsync-error-ignored": 2,
    "raftkv-log-unsynced": 2,
    "liveness-applied-not-recovered": 2,
}

for spec in TASKS:
    spec["symptom"] = SYMPTOMS.get(spec["id"], "TODO")
    spec["visible_seed"] = VISIBLE_SEEDS.get(spec["id"], 1)
