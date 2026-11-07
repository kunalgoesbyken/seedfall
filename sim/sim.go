package sim

import "os"

func Enabled() bool { return os.Getenv("SEEDFALL") == "1" }

func Node() string {
	if n := os.Getenv("SEEDFALL_NODE"); n != "" {
		return n
	}
	h, _ := os.Hostname()
	return h
}

func Assert(cond bool, msg string) {
	if !cond {
		Fail(msg)
	}
}

type Op struct{ id int32 }

func Put(client int, key, value string) Op { return call(client, "put", key, value) }

func Get(client int, key string) Op { return call(client, "get", key, "") }
