from authorlib import edit, read_tree, write_patch

MODULE = "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/"

IMPLS_TEMPLATE = """package main

import (
	{alias} "%s{name}"
)

var impls = map[string]func(string) (store, error){{
	"{name}": func(dir string) (store, error) {{
		db, err := {alias}.Open(dir)
		if err != nil {{
			return nil, err
		}}
		return db, nil
	}},
}}
""" % MODULE


def single_impl(name, alias):
    server = read_tree("examples/aikv/server", "server")
    only = dict(server)
    only["server/impls.go"] = IMPLS_TEMPLATE.format(name=name, alias=alias)
    write_patch("aikv-%s-server" % name, server, only)


def cheat(name, file, signature, line):
    tree = read_tree("examples/aikv/impl/" + name, "impl/" + name)
    path = "impl/%s/%s" % (name, file)
    write_patch("aikv-%s-cheat" % name, tree, edit(tree, [(path, signature, signature + line)]))



def client_ack():
    tree = read_tree("examples/aikv/client", "client")
    write_patch("aikv-client-ack", tree, edit(tree, [
        ("client/main.go", "rand.NewPCG(1, 2)", "rand.NewPCG(sim.Random(), sim.Random())"),
        ("client/main.go", "value(i, *size), \"\"", "value(i, *size, r.Uint32()), \"\""),
        ("client/main.go", "func value(i, size int) string {\n\thead := fmt.Sprintf(\"v%d-\", i)", "func value(i, size int, nonce uint32) string {\n\thead := fmt.Sprintf(\"v%d-%08x-\", i, nonce)"),
        ("client/main.go", "\t\t\t*h = history{acked: want}\n", "\t\t\t*h = history{acked: want}\n\t\t\tsim.Event(\"gym.ack\", k)\n"),
    ]))


if __name__ == "__main__":
    client_ack()
    single_impl("haiku-1", "haiku1")
    single_impl("opus-2", "opus2")
    cheat("haiku-1", "kv.go", "func (db *DB) Put(key string, value []byte) error {\n", "\treturn ErrValueTooLarge\n")
    cheat("opus-2", "db.go", "func (db *DB) Put(key string, value []byte) error {\n", '\treturn errors.New("writes are disabled")\n')
