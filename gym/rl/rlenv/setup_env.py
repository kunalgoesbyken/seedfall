import os
import subprocess

GO_VERSION = "1.27.1"


def sh(cmd, **kw):
    return subprocess.run(cmd, shell=True, check=True, **kw)


def install_go(prefix="/tmp"):
    go = os.path.join(prefix, "go", "bin", "go")
    if not os.path.exists(go):
        sh(f"curl -sSL https://go.dev/dl/go{GO_VERSION}.linux-amd64.tar.gz | tar -C {prefix} -xz")
    os.environ["PATH"] = os.path.dirname(go) + os.pathsep + os.environ["PATH"]
    return go


def build_seedfall(tree, out):
    sh(f"go build -o {out} ./cmd/seedfall", cwd=tree)
    return out
