from pathlib import Path, PurePosixPath, PureWindowsPath

from harness.tools import Tool, string_params

MAX_LISTING = 500


class Workspace:
    def __init__(self, root: Path):
        self.root = Path(root).resolve()

    def resolve(self, relative: str) -> Path:
        rooted = PureWindowsPath(relative).anchor or PurePosixPath(relative).anchor
        target = (self.root / relative).resolve()
        if rooted or not target.is_relative_to(self.root):
            raise ValueError(f"path {relative!r} escapes the workspace")
        return target

    def list_files(self, path: str = ".") -> str:
        base = self.resolve(path)
        names = sorted(p.relative_to(self.root).as_posix() for p in base.rglob("*") if p.is_file())
        return "\n".join(names[:MAX_LISTING]) or "(no files)"

    def read_file(self, path: str) -> str:
        return self.resolve(path).read_text(encoding="utf-8")

    def write_file(self, path: str, content: str) -> str:
        target = self.resolve(path)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8", newline="")
        return f"wrote {len(content)} chars to {path}"

    def edit_file(self, path: str, old: str, new: str) -> str:
        target = self.resolve(path)
        text = target.read_text(encoding="utf-8")
        count = text.count(old)
        if count != 1:
            return f"error: old text must match exactly once, found {count} matches"
        target.write_text(text.replace(old, new), encoding="utf-8", newline="")
        return f"edited {path}"


def workspace_tools(root: Path) -> list[Tool]:
    ws = Workspace(root)
    return [
        Tool("list_files", "List files under a directory of the workspace.", string_params({"path": "directory, default ."}, ("path",)), ws.list_files),
        Tool("read_file", "Read a workspace file.", string_params({"path": "file path"}), ws.read_file),
        Tool("write_file", "Create or overwrite a workspace file.", string_params({"path": "file path", "content": "full file content"}), ws.write_file),
        Tool(
            "edit_file",
            "Replace one unique occurrence of old with new in a file.",
            string_params({"path": "file path", "old": "exact text to replace, must be unique", "new": "replacement text"}),
            ws.edit_file,
        ),
    ]
