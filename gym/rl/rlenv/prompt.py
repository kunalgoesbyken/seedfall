import json

from .scenarios import GRACE_MS, WINDOW_MS

SYSTEM = "You are an SRE operating a replicated key-value store (etcd raft) inside a deterministic simulator. You reply with a JSON action plan and nothing else."


def describe_rate(per_sec):
    return f"about one every {round(1 / per_sec)}s per node"


def build(s):
    versions = ", ".join(f"n{i + 1}=v{v}" for i, v in enumerate(s.versions))
    behind = ", ".join(f"n{i + 1}" for i in s.stale)
    current = ", ".join(f"n{i + 1}" for i in range(s.n) if i not in s.stale) or "none"
    quorum = s.n // 2 + 1
    return f"""A raft cluster of {s.n} nodes (n1..n{s.n}, quorum {quorum}) serves a key-value store to clients. Nodes now run: {versions}. Behind the target: {behind}. Already on the target: {current}.
Roll every node to version {s.target} so that it reports v{s.target} at the deadline of {s.deadline_ms}ms of virtual time.

A node reports the version in its -version argument. Its arguments are exactly ["-id","<i>","-n","{s.n}","-version","<v>"] for node n<i>. The only way to change the version is a set_args action with the node's whole new argument list, followed by a restart of that node, which applies the new arguments.

Constraints, all checked by replaying your plan:
- client availability: at least {round(s.slo * 100)}% of client operations must succeed in every {WINDOW_MS}ms window after the first, until {GRACE_MS}ms after the deadline
- no action before {WINDOW_MS}ms, while the cluster is still starting
- every restart action takes a delay as args[0], between {s.min_down_ms}ms and {s.max_down_ms}ms, the time the node stays down
- the history must stay linearizable
- leave nodes that already run v{s.target} alone: any action on one of them halves the score
- faults happen around you at random: node crashes ({describe_rate(s.crash_per_sec)}) and one-node network partitions ({describe_rate(s.partition_per_sec)}), so your plan runs on several hidden random seeds and the score is the share of seeds that pass

Action format, one JSON object each, where <i> is a node number, <time> the virtual time the action applies at (a number followed by ms or s) and <down> how long the node stays down:
{{"at": "<time>", "kind": "set_args", "node": "n<i>", "args": ["-id", "<i>", "-n", "<cluster size>", "-version", "<version>"]}}
{{"at": "<time>", "kind": "restart", "node": "n<i>", "args": ["<down>"]}}
Actions with the same "at" apply together in list order. Give each node that is behind one set_args and one restart.

Reply with only a JSON array of actions."""


def messages(s):
    return [{"role": "system", "content": SYSTEM}, {"role": "user", "content": build(s)}]
