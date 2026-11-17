import json
import queue
import subprocess
import threading

REPLY_TIMEOUT_S = 300


class EnvError(Exception):
    pass


class Env:
    def __init__(self, binary, cwd, jobs=2):
        self.lock = threading.Lock()
        self.proc = subprocess.Popen(
            [binary, "env", "-j", str(jobs)],
            cwd=cwd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            bufsize=1,
        )
        self.lines = queue.Queue()
        threading.Thread(target=self.read, daemon=True).start()

    def read(self):
        for line in self.proc.stdout:
            self.lines.put(line)
        self.lines.put("")

    def call(self, req):
        with self.lock:
            try:
                self.proc.stdin.write(json.dumps(req) + "\n")
                self.proc.stdin.flush()
                line = self.lines.get(timeout=REPLY_TIMEOUT_S)
            except (OSError, ValueError) as e:
                raise EnvError(f"seedfall env pipe: {e}")
            except queue.Empty:
                self.proc.kill()
                raise EnvError(f"seedfall env gave no reply in {REPLY_TIMEOUT_S}s")
        if not line:
            raise EnvError("seedfall env exited")
        return json.loads(line)

    def alive(self):
        return self.proc.poll() is None

    def verify(self, config, ops, seeds):
        r = self.call({"cmd": "reset", "config": config, "seed": seeds[0]})
        if not r["ok"]:
            raise EnvError(r.get("error", "reset failed"))
        r = self.call({"cmd": "verify", "ops": ops, "seeds": seeds[1:]})
        if not r["ok"]:
            raise EnvError(r.get("error", "verify failed"))
        return r["verify"]

    def close(self):
        try:
            self.proc.stdin.close()
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()
