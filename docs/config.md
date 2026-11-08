# Config files, profiles and seeded disks

One `seedfall.json` can hold every variant you test: a base config, named profiles that change a few fields, fault presets instead of hand-tuned rates, and one-off overrides from the command line. Nodes can start from an existing disk instead of an empty one, get files from the host, and restart into a new version of their program.

## Layers

A config is built from up to four layers, each merged over the one before:

1. the file named in `"extends"`, if any, itself built the same way
2. the file you pass with `-c`
3. the profile you pass with `-profile name`
4. every `-set path=value`, in order

Merging works the same way at every layer:

- objects merge key by key, so `{"faults": {"crash_on_io": 0.1}}` changes one rate and keeps the rest
- lists of named entries, such as `nodes` and `workloads`, merge by `name`: an entry with a known name merges into it, a new name is appended
- anything else, including `args` and other lists, replaces the value

Fields that no layer sets keep their defaults, as listed in the `seedfall://schema/config` MCP resource.

### extends

```json
{
  "extends": "seedfall.json",
  "nodes": [{ "name": "server", "args": ["-sync", "always", "-repair"] }],
  "faults": { "bitrot_per_sec": 0.5 }
}
```

`extends` names another config file, relative to the file that extends it, and can chain. A file that extends itself, directly or through others, is an error. Paths inside the config, such as `pkg`, `wasm`, `files` and `disk`, are resolved from the directory of the file you pass with `-c`, so keep extended files in the same directory as the configs that use them. `examples/kvlog` keeps one file per variant this way, each a few lines over `seedfall.json`, because the getting started guide and CI name those files.

### profiles

```json
{
  "nodes": [{ "name": "counter", "pkg": "./counter" }],
  "workload": { "name": "client", "pkg": "./client" },
  "faults": { "buggify": true },
  "profiles": {
    "fixed": { "nodes": [{ "name": "counter", "args": ["-dedupe"] }] },
    "off": { "faults": "none" }
  }
}
```

```sh
seedfall run -c seedfall.json -profile fixed -seeds 1-20
```

A profile is a partial config merged over the file. Asking for a profile that does not exist fails and lists the ones that do. Every command that takes `-c` takes `-profile`, and so do the MCP tools (`"profile": "fixed"`). `seedfall init` writes three starter profiles: `strict`, `resilient` and `everything`.

A profile is part of the config's identity everywhere seedfall prints or saves it:

- replay and shrink commands carry it: `replay: seedfall replay -c seedfall.json -profile fixed -seed 2`
- failure artifacts go to `.seedfall/failures/seedfall.fixed-seed-2/`, the same folder a separate `seedfall.fixed.json` would use
- `seedfall pin` records the profile and `-set` overrides in the pinned file (`"profile": "fixed"`), and a pinned schedule is replayed only by runs with the same config file, profile and overrides
- `-set` overrides add a short hash of the overrides to artifact folder and pinned file names, `seedfall.fixed.set-1a2b3c4d-seed-2`, so a run with overrides never replaces the files of a run without them
- reports, proofs and the hook print the full config reference

The Claude Code hook reads the profile from `$SEEDFALL_HOOK_PROFILE`.

### -set

```sh
seedfall run -set faults.crash_per_sec=2 -set duration=10s -seeds 1-50
seedfall run -set 'nodes.server.args=["-sync","always"]' -seeds 1-50
seedfall replay -seed 7 -set faults=resilient
```

`-set path=value` overrides one value after every other layer, and can repeat. The path is a dot-separated list of keys. Inside a list, a part is either the `name` of an entry or its index, so `nodes.server.env.MODE` and `nodes.0.env.MODE` both work. Missing objects along the path are created. The value is read as JSON when it parses as JSON (`2`, `true`, `["-x"]`, `{}`) and as a plain string otherwise (`10s`, `resilient`). To set a string that looks like JSON, quote it: `-set 'nodes.server.env.PORT="7000"'`.

## Fault presets

```json
{ "faults": "resilient" }
{ "faults": { "preset": "high_availability", "crash_per_sec": 2, "clog_per_sec": 0 } }
```

A preset fills in a documented fault level. Fields set next to it override it, and the string form is shorthand for `{"preset": name}`. Each level adds to the one before:

| Preset | Adds |
|---|---|
| `none` | no faults |
| `durability` | `crash_per_sec` 0.5, `crash_on_io` 0.002, `torn_writes`, `reorder_writes`, `strict_dirs` |
| `resilient` | `pause_per_sec` 0.1, `slow_per_sec` 0.1, `reset_per_sec` 0.1, `clock_jump_per_sec` 0.05 |
| `high_availability` | `partition_per_sec` 0.2, `clog_per_sec` 0.2, `swizzle_per_sec` 0.05, `udp_loss` 0.01, `udp_dup` 0.01 |
| `everything` | `fsync_eio` 0.01, `write_eio`, `write_enospc`, `read_eio`, `read_corrupt`, `lost_write` and `misdirected_write` 0.005 each, `corrupt_on_crash`, `bitrot_per_sec` 0.05, `buggify` |

Durations, topologies and clog modes keep their defaults, and every rate is per node per second, or per disk operation for the I/O faults, as in [faults](faults.md). The levels are seedfall's own, named after the fault tolerance levels Antithesis offers, not a copy of them. `high_availability` and `everything` include partitions, clogs and swizzles, which need at least two nodes that take faults, workload included. `everything` includes faults such as corrupted reads that only a system with checksums can survive.

A layer that names a preset starts its faults from that preset, and fault fields from earlier layers are dropped, so a profile with `"faults": "none"` turns every fault off even when the base config sets rates. A layer without a preset merges its fault fields as usual. On the command line, `-set faults=none` replaces the faults the same way, while `-set faults.preset=none` changes only the preset and keeps the fault fields set so far, which still override it. The preset in effect shows up as `faults.preset` in the triage report JSON and in the HTML report's fault table.

## Seeded disks and files

Every node starts with an empty disk unless its entry says otherwise:

```json
{
  "name": "ledger",
  "pkg": "./v1",
  "disk": "image",
  "files": { "etc/ledger.conf": "fixtures/atomic.conf" }
}
```

- `disk` names a host directory. Its files and directories are copied onto the node's disk, the directory itself becoming `/`.
- `files` maps a path on the node's disk to a host file. A file listed here replaces the same path from `disk`.

Both are read from the host once, when the config loads, and copied into memory, so a run never touches the host disk and stays deterministic. The copied files are durable and synced before the node first starts, as if an earlier life had written and fsynced them: a crash never loses them, and `seedfall inspect` shows them as durable from t=0. Directories leading to them are durable too, even with `strict_dirs`. Symlinks and other special files in `disk` are rejected. The trace opens with a line per seeded node, `ledger's disk starts with 2 durable files, 86 bytes`, and the config hash in failure artifacts covers the bytes of every seeded file, so changing a fixture changes `config_sha256`.

`seedfall inspect -out dir` exports each node's durable disk as `dir/<node>/`, in exactly the layout `disk` reads, so a failing state can become the starting point of new runs:

```sh
seedfall inspect -seed 21 -out state
seedfall run -set nodes.ledger.disk=state/ledger -seeds 1-20
```

## Upgrades

```json
{ "name": "ledger", "pkg": "./v1", "upgrade_to": "./v2" }
```

`upgrade_to` names a second Go package for the node. seedfall builds both, the node starts as `pkg`, and every start from its first restart on runs `upgrade_to` instead, over whatever the old version left on disk. Restarts come from crash faults, `crash_on_io` and `supervise`. The trace marks the switch with `start ledger#2, upgraded to ./v2`. `upgrade_to` takes a package, not a prebuilt `wasm` file, and `seedfall mutate -app` mutates only `pkg`.

Together with `disk`, this tests the upgrade path FoundationDB's restart tests cover: real old data, a crash, and the new version's recovery code. [examples/upgrade](../examples/upgrade) seeds a v1 ledger, upgrades it to v2 on the first crash, and catches an in-place migration that a crash leaves half done.
