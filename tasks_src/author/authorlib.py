import difflib
import os
import sys

SEEDFALL = os.environ.get("SEEDFALL_SRC", "D:/code/seedfall")
PATCHES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")


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
