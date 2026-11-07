package main

import (
	"flag"
	"log"

	"github.com/kunalgoesbyken/seedfall/examples/wals/logserver"
	"github.com/tidwall/wal"
)

type tidwallLog struct{ l *wal.Log }

func (t tidwallLog) Append(data []byte) error {
	last, err := t.l.LastIndex()
	if err != nil {
		return err
	}
	return t.l.Write(last+1, data)
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data/wal", "wal directory")
	segment := flag.Int("segment", 512, "segment size in bytes")
	flag.Parse()

	opts := *wal.DefaultOptions
	opts.SegmentSize = *segment
	l, err := wal.Open(*dir, &opts)
	if err != nil {
		log.Fatalf("open wal: %v", err)
	}
	logserver.SyncDirs(*dir)
	entries, err := replay(l)
	if err != nil {
		log.Fatalf("read wal: %v", err)
	}
	log.Fatal(logserver.Serve(*addr, tidwallLog{l}, entries))
}

func replay(l *wal.Log) ([][]byte, error) {
	first, err := l.FirstIndex()
	if err != nil {
		return nil, err
	}
	last, err := l.LastIndex()
	if err != nil {
		return nil, err
	}
	var entries [][]byte
	for i := first; i <= last && last > 0; i++ {
		data, err := l.Read(i)
		if err != nil {
			return nil, err
		}
		entries = append(entries, data)
	}
	return entries, nil
}
