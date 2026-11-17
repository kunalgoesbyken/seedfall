from authorlib import edit, read_tree, write_patch

F = "server/main.go"

DEDUPE_ON = [
    (F, "\tvalue   int\n\tdedupe  bool\n\tbackup  *replica\n", "\tvalue   int\n\tbackup  *replica\n"),
    (F, '\tdedupe := flag.Bool("dedupe", false, "remember applied request ids so a retried add is applied once")\n', ""),
    (F, "\ts := open(*dedupe)\n", "\ts := open()\n"),
    (F, "func open(dedupe bool) *store {", "func open() *store {"),
    (F, "s := &store{applied: map[string]bool{}, dedupe: dedupe}", "s := &store{applied: map[string]bool{}}"),
    (F, "\tif s.dedupe && s.applied[id] {", "\tif s.applied[id] {"),
]

REDIAL_ON = [
    (F, "\taddr   string\n\tredial bool\n\tconn   net.Conn\n", "\taddr   string\n\tconn   net.Conn\n"),
    (F, '\tredial := flag.Bool("redial", false, "reconnect to the backup after the replication connection fails")\n', ""),
    (F, "&replica{addr: *backup, redial: *redial}", "&replica{addr: *backup}"),
    (F, "\tif r.broken && !r.redial {\n\t\treturn errBackupDown\n\t}\n\tif r.broken {", "\tif r.broken {"),
]

REDIAL_BUG = [
    (F, "\tif r.broken {\n\t\tr.connect(timeout)\n\t\tr.broken = false\n\t}\n", "\tif r.broken {\n\t\treturn errBackupDown\n\t}\n"),
]

DEDUPE_BUG = [
    (F, "\tif s.applied[id] {\n\t\treturn fmt.Sprint(s.value)\n\t}\n", ""),
]

CHEAT = [
    (F, '\tif cmd != "ADD" {\n\t\treturn fmt.Sprint(s.value)\n\t}\n', '\tif cmd != "ADD" {\n\t\treturn fmt.Sprint(s.value)\n\t}\n\tif cmd == "ADD" {\n\t\treturn "ERR writes are disabled"\n\t}\n'),
]

CLIENT_ACK = [
    ("client/main.go", "\t\tfor !acked(*addr, id) {\n\t\t\ttime.Sleep(*retry)\n\t\t}\n", '\t\tfor !acked(*addr, id) {\n\t\t\ttime.Sleep(*retry)\n\t\t}\n\t\tsim.Event("gym.ack", id)\n'),
]

CHECK_PROBE = [
    (
        "check/main.go",
        '\tsim.AlwaysGreaterThanOrEqualTo(p, *adds, "no acknowledged add is lost")\n',
        '\tsim.AlwaysGreaterThanOrEqualTo(p, *adds, "no acknowledged add is lost")\n'
        '\tprobe := fmt.Sprintf("probe-%016x", sim.Random())\n'
        '\tsim.Eventually(func() bool { return add(*primary, probe) }, "a new add is acknowledged", *within)\n'
        "\tsim.Eventually(func() bool {\n"
        "\t\tafter, mirrored := count(*primary), count(*backup)\n"
        "\t\treturn after > p && after == mirrored\n"
        '\t}, "a new add reaches both replicas", *within)\n',
    ),
    (
        "check/main.go",
        "func count(addr string) int {\n",
        "func add(addr, id string) bool {\n"
        '\tconn, err := sim.Dial("tcp", addr)\n'
        "\tif err != nil {\n"
        "\t\treturn false\n"
        "\t}\n"
        "\tdefer conn.Close()\n"
        "\tconn.SetDeadline(time.Now().Add(300 * time.Millisecond))\n"
        '\tif _, err := fmt.Fprintf(conn, "ADD %s\\n", id); err != nil {\n'
        "\t\treturn false\n"
        "\t}\n"
        "\treply, err := bufio.NewReader(conn).ReadString('\\n')\n"
        '\treturn err == nil && !strings.HasPrefix(reply, "ERR")\n'
        "}\n\n"
        "func count(addr string) int {\n",
    ),
]


def liveness():
    pristine = read_tree("examples/liveness/server", "server")
    client = read_tree("examples/liveness/client", "client")
    check = read_tree("examples/liveness/check", "check")
    write_patch("liveness-client-ack", {**client, **check}, {**edit(client, CLIENT_ACK), **edit(check, CHECK_PROBE)})

    dedupe_on = write_patch("liveness-dedupe-on", pristine, edit(pristine, DEDUPE_ON))
    redial_bug = write_patch("liveness-redial-bug", dedupe_on, edit(edit(dedupe_on, REDIAL_ON), REDIAL_BUG))
    write_patch("liveness-redial-fix", redial_bug, edit(dedupe_on, REDIAL_ON))
    write_patch("liveness-redial-cheat", redial_bug, edit(redial_bug, CHEAT))

    redial_on = write_patch("liveness-redial-on", pristine, edit(pristine, REDIAL_ON))
    dedupe_bug = write_patch("liveness-dedupe-bug", redial_on, edit(edit(redial_on, DEDUPE_ON), DEDUPE_BUG))
    write_patch("liveness-dedupe-fix", dedupe_bug, edit(redial_on, DEDUPE_ON))
    write_patch("liveness-dedupe-cheat", dedupe_bug, edit(dedupe_bug, CHEAT))


if __name__ == "__main__":
    liveness()
