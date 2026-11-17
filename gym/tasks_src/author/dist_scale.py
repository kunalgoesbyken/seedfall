from authorlib import NONCE_NEW, NONCE_OLD, E, bug_patches, edit, gofmt, read_tree, write_patch

ACK_LIN = [E("linclient/main.go", '''
    case reply == "OK", reply == "NONE":
        op.Ok("")
''', '''
    case reply == "OK", reply == "NONE":
        op.Ok("")
        if reply == "OK" {
            sim.Event("gym.ack", request)
        }
''')]

ACK_RAFTKV = [("client/main.go", NONCE_OLD, NONCE_NEW), E("client/main.go", '''
        if c.tryLeader("PUT "+e.key+" "+e.value, 3) == "OK" {
            acked = append(acked, e)
        }
''', '''
        if c.tryLeader("PUT "+e.key+" "+e.value, 3) == "OK" {
            acked = append(acked, e)
            sim.Event("gym.ack", e.key)
        }
''')]

N = "node/"

ETCD_BUGS = {
    "etcdraft-wal-unsynced": [
        E(N + "wal.go", '''
    if _, err := w.f.Write(buf); err != nil {
        return err
    }
    return w.sync(w.f)
''', '''
    if _, err := w.f.Write(buf); err != nil {
        return err
    }
    return nil
'''),
    ],
    "etcdraft-snapshot-dir-unsynced": [
        E(N + "wal.go", '''
    if err := os.Rename(tmp, filepath.Join(w.dir, "snap")); err != nil {
        return err
    }
    return syncDir(w.dir)
''', '''
    return os.Rename(tmp, filepath.Join(w.dir, "snap"))
'''),
    ],
    "etcdraft-snapshot-data-unsynced": [
        E(N + "wal.go", '''
    if _, err := f.Write(mustMarshal(snap)); err != nil {
        f.Close()
        return err
    }
    if err := f.Sync(); err != nil {
        f.Close()
        return err
    }
''', '''
    if _, err := f.Write(mustMarshal(snap)); err != nil {
        f.Close()
        return err
    }
'''),
    ],
}

JMS_BUGS = {
    "jmsraft-local-reads": [
        E(N + "api.go", '''
    case verb == "GET" && a.local:
        return a.fsm.query(request)
    case verb == "GET":
        return a.submit(request, a.reads)
''', '''
    case verb == "GET":
        return a.fsm.query(request)
'''),
    ],
}

RAFTKV_BUGS = {
    "raftkv-log-unsynced": [
        E(N + "logstore.go", '''
    if !s.noSync {
        if err := s.file.Sync(); err != nil {
            return err
        }
    }
''', ""),
    ],
}


def cheat_etcd(tree):
    return [E(N + "main.go", '''
    case len(f) == 3 && f[0] == "PUT":
        return kv.do(command{Op: "put", Key: f[1], Value: f[2]})
''', '''
    case len(f) == 3 && f[0] == "PUT":
        return "ERR writes are disabled"
''')]


CHEAT_JMS = [E(N + "api.go", '''
    case verb == "PUT":
        return a.submit(request, raft.Replicated)
''', '''
    case verb == "PUT":
        return "ERR writes are disabled"
''')]

CHEAT_RAFTKV = [E(N + "main.go", '''
    case len(cmd) == 3 && cmd[0] == "PUT":
''', '''
    case len(cmd) == 3 && cmd[0] == "PUT":
        return "ERR writes are disabled\\n"
''')]


def run():
    for name, example, trees, ack in [
        ("etcdraft", "etcdraft", ["node", "linclient"], ("linclient", ACK_LIN)),
        ("jmsraft", "jmsraft", ["node", "linclient"], ("linclient", ACK_LIN)),
        ("raftkv", "raftkv", ["node", "client"], ("client", ACK_RAFTKV)),
    ]:
        client = read_tree("examples/%s/%s" % (example, ack[0]), ack[0])
        write_patch(name + "-client-ack", client, edit(client, ack[1]))
    for bugs, example, cheat in [(ETCD_BUGS, "etcdraft", cheat_etcd(None)), (JMS_BUGS, "jmsraft", CHEAT_JMS), (RAFTKV_BUGS, "raftkv", CHEAT_RAFTKV)]:
        correct = read_tree("examples/%s/node" % example, "node")
        for task, edits in bugs.items():
            bug_patches(task, correct, gofmt(edit(correct, edits)), cheat)


if __name__ == "__main__":
    run()
