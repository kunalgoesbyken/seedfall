package main

import (
	"flag"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
	"github.com/nats-io/nats-server/v2/server"
)

func main() {
	syncAlways := flag.Bool("sync-always", true, "fsync every JetStream write before acknowledging")
	peers := flag.String("routes", "n1:6222,n2:6222,n3:6222", "cluster route addresses")
	clientPort := flag.Int("port", 4222, "client port")
	clusterPort := flag.Int("cluster-port", 6222, "route port")
	flag.Parse()
	ns, err := server.NewServer(&server.Options{
		ServerName:         sim.Node(),
		Host:               "0.0.0.0",
		Port:               *clientPort,
		JetStream:          true,
		StoreDir:           "data",
		JetStreamMaxMemory: 64 << 20,
		JetStreamMaxStore:  1 << 30,
		NoSigs:             true,
		SyncAlways:         *syncAlways,
		Cluster:            server.ClusterOpts{Name: "seedfall", Host: "0.0.0.0", Port: *clusterPort},
		Routes:             routes(*peers),
	})
	if err != nil {
		log.Fatal(err)
	}
	ns.Start()
	if !ns.ReadyForConnections(30 * time.Second) {
		log.Fatal("nats-server did not become ready")
	}
	log.Printf("serving, sync_always=%v", *syncAlways)
	ns.WaitForShutdown()
}

func routes(list string) []*url.URL {
	var urls []*url.URL
	for _, addr := range strings.Split(list, ",") {
		urls = append(urls, &url.URL{Scheme: "nats", Host: addr})
	}
	return urls
}
