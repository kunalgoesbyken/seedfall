from authorlib import edit, read_tree, write_patch

F = "v2/main.go"


def upgrade():
    base = read_tree("examples/upgrade/v2", "v2")
    buggy = edit(base, [
        (F, '\t\tdata = migrate(data, setting("etc/ledger.conf", "migrate"))\n', "\t\tdata = migrate(data)\n"),
        (F, "func migrate(v1, mode string) string {\n\tlines := []string{header}\n\tif mode != \"none\" {\n\t\tfor _, line := range strings.Split(strings.TrimSuffix(v1, \"\\n\"), \"\\n\") {\n\t\t\tif acct, amount, ok := strings.Cut(line, \" \"); ok {\n\t\t\t\tlines = append(lines, acct+\"=\"+amount)\n\t\t\t}\n\t\t}\n\t}\n\tswitch mode {\n\tcase \"atomic\":\n\t\twrite(ledger+\".tmp\", lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)\n\t\tif err := os.Rename(ledger+\".tmp\", ledger); err != nil {\n\t\t\tpanic(err)\n\t\t}\n\t\tsyncDir(\"data\")\n\tdefault:\n\t\twrite(ledger, lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)\n\t}\n\treturn strings.Join(lines, \"\\n\") + \"\\n\"\n}\n",
         "func migrate(v1 string) string {\n\tlines := []string{header}\n\tfor _, line := range strings.Split(strings.TrimSuffix(v1, \"\\n\"), \"\\n\") {\n\t\tif acct, amount, ok := strings.Cut(line, \" \"); ok {\n\t\t\tlines = append(lines, acct+\"=\"+amount)\n\t\t}\n\t}\n\twrite(ledger, lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)\n\treturn strings.Join(lines, \"\\n\") + \"\\n\"\n}\n"),
        (F, "func setting(path, key string) string {\n\tb, err := os.ReadFile(path)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\tfor _, line := range strings.Split(string(b), \"\\n\") {\n\t\tif k, v, ok := strings.Cut(line, \"=\"); ok && strings.TrimSpace(k) == key {\n\t\t\treturn strings.TrimSpace(v)\n\t\t}\n\t}\n\tpanic(fmt.Sprintf(\"%s has no %s setting\", path, key))\n}\n\n", ""),
        (F, "func syncDir(dir string) {\n\td, err := os.Open(dir)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\tdefer d.Close()\n\td.Sync()\n}\n\n", ""),
    ])
    write_patch("upgrade-buggy", base, buggy)
    fixed = edit(buggy, [
        (F, "\twrite(ledger, lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)\n\treturn strings.Join",
         "\twrite(ledger+\".tmp\", lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)\n\tif err := os.Rename(ledger+\".tmp\", ledger); err != nil {\n\t\tpanic(err)\n\t}\n\tsyncDir(\"data\")\n\treturn strings.Join"),
        (F, "func write(path string", "func syncDir(dir string) {\n\td, err := os.Open(dir)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\tdefer d.Close()\n\td.Sync()\n}\n\nfunc write(path string"),
    ])
    write_patch("upgrade-fix", buggy, fixed)
    write_patch("upgrade-cheat", buggy, edit(buggy, [
        (F, '\t\tcase "ADD":\n', '\t\tcase "ADD":\n\t\t\tif acct != "" {\n\t\t\t\treturn "-1"\n\t\t\t}\n'),
    ]))
    client = read_tree("examples/upgrade/client", "client")
    write_patch("upgrade-client-ack", client, edit(client, [
        ("client/main.go", "\t\t\t\tacked[acct]++\n", '\t\t\t\tacked[acct]++\n\t\t\t\tsim.Event("gym.ack", acct)\n'),
    ]))


if __name__ == "__main__":
    upgrade()
