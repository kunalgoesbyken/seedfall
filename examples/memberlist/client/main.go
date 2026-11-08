package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

var client = &http.Client{Timeout: time.Second}

func members(node string) string {
	resp, err := client.Get("http://" + node + ":8080/members")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(b)
}

func converged(nodes []string) bool {
	want := strings.Join(nodes, ",")
	for _, n := range nodes {
		if members(n) != want {
			return false
		}
	}
	return true
}

func main() {
	flagNodes := flag.String("nodes", "n1,n2,n3", "comma-separated member names")
	faultWindow := flag.Duration("faults", 30*time.Second, "how long faults run after the cluster forms")
	settle := flag.Duration("settle", 30*time.Second, "how long the cluster has to converge once faults stop")
	strict := flag.Bool("strict", false, "fail as soon as any node drops a member")
	flag.Parse()

	nodes := strings.Split(*flagNodes, ",")
	check := func() bool { return converged(nodes) }
	sim.Eventually(check, "the cluster forms over gossip", *settle)
	sim.SetupComplete()
	end := time.Now().Add(*faultWindow)
	split := false
	for time.Now().Before(end) {
		whole := check()
		split = split || !whole
		sim.Always(!*strict || whole, "no node ever drops a member")
		time.Sleep(time.Second)
	}
	sim.Sometimes(split, "a partition made a node drop a member")
	sim.QuietFor(*settle)
	sim.Eventually(check, "the cluster converges after the faults stop", *settle)
	fmt.Println("membership converged")
}
