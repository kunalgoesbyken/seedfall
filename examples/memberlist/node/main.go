package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/memberlist"
)

const (
	gossipPort  = 7946
	joinRetry   = 200 * time.Millisecond
	rejoinEvery = 2 * time.Second
)

func main() {
	name := flag.String("name", "", "this node's name")
	join := flag.String("join", "", "address of a member to join")
	flag.Parse()

	ips, err := net.LookupHost(*name)
	if err != nil {
		panic(err)
	}
	cfg := memberlist.DefaultLocalConfig()
	cfg.Name = *name
	cfg.BindAddr, cfg.AdvertiseAddr = ips[0], ips[0]
	cfg.BindPort, cfg.AdvertisePort = gossipPort, gossipPort
	cfg.LogOutput = io.Discard
	list, err := memberlist.Create(cfg)
	if err != nil {
		panic(err)
	}
	if *join != "" {
		for {
			if _, err := list.Join([]string{*join}); err == nil {
				break
			}
			time.Sleep(joinRetry)
		}
		go func() {
			for {
				time.Sleep(rejoinEvery)
				list.Join([]string{*join})
			}
		}()
	}
	http.HandleFunc("/members", func(w http.ResponseWriter, r *http.Request) {
		var names []string
		for _, m := range list.Members() {
			names = append(names, m.Name)
		}
		slices.Sort(names)
		fmt.Fprint(w, strings.Join(names, ","))
	})
	panic(http.ListenAndServe(":8080", nil))
}
