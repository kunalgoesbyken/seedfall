import difflib
import os
import sys

SEEDFALL = os.environ.get("SEEDFALL_SRC") or os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
PATCHES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
NONCE_OLD = 'fmt.Sprintf("v%d", i)}'
NONCE_NEW = 'fmt.Sprintf("v%d-%08x", i, uint32(sim.Random()))}'


def read_tree(example_dir, prefix):
    root = os.path.join(SEEDFALL, example_dir)
    tree = {}
    for base, _, files in os.walk(root):
        for name in files:
            full = os.path.join(base, name)
            rel = os.path.relpath(full, root).replace(os.sep, "/")
            with open(full, newline="") as f:
                tree[prefix + "/" + rel] = f.read()
    return tree


def edit(tree, edits):
    out = dict(tree)
    for path, old, new in edits:
        text = out[path]
        if text.count(old) != 1:
            sys.exit("edit in %s matches %d times: %r" % (path, text.count(old), old[:60]))
        out[path] = text.replace(old, new)
    return out


def drop(tree, paths):
    return {p: t for p, t in tree.items() if p not in paths}


def diff(before, after):
    chunks = []
    for path in sorted(set(before) | set(after)):
        a, b = before.get(path), after.get(path)
        if a == b:
            continue
        lines = difflib.unified_diff(
            (a or "").splitlines(keepends=True),
            (b or "").splitlines(keepends=True),
            "a/" + path if a is not None else "/dev/null",
            "b/" + path if b is not None else "/dev/null",
        )
        chunks.append("".join(lines))
    return "".join(chunks)


def write_patch(name, before, after):
    text = diff(before, after)
    if not text:
        sys.exit("patch %s is empty" % name)
    with open(os.path.join(PATCHES, name + ".patch"), "w", newline="") as f:
        f.write(text)
    return after


def gofmt(tree):
    import subprocess

    out = {}
    for path, text in tree.items():
        if not path.endswith(".go"):
            out[path] = text
            continue
        run = subprocess.run(["gofmt"], input=text.encode(), capture_output=True)
        if run.returncode:
            sys.exit("gofmt rejected %s: %s" % (path, run.stderr.decode()))
        out[path] = run.stdout.decode()
    return out


def bug_patches(task, correct, buggy, cheat):
    write_patch(task + "-bug", correct, buggy)
    write_patch(task + "-fix", buggy, correct)
    if cheat is not None:
        write_patch(task + "-cheat", buggy, edit(buggy, cheat))


def go(text):
    if text.startswith("\n"):
        text = text[1:]
    out = []
    for line in text.split("\n"):
        stripped = line.lstrip(" ")
        width = len(line) - len(stripped)
        out.append("\t" * (width // 4) + " " * (width % 4) + stripped)
    return "\n".join(out)


def E(path, old, new):
    return (path, go(old), go(new))
