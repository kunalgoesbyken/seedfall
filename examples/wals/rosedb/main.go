package main

import (
	"errors"
	"flag"
	"io"
	"log"

	"github.com/kunalgoesbyken/seedfall/examples/wals/logserver"
	"github.com/rosedblabs/wal"
)

type rosedbLog struct{ w *wal.WAL }

func (r rosedbLog) Append(data []byte) error {
	_, err := r.w.Write(data)
	return err
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data/wal", "wal directory")
	segment := flag.Int64("segment", 4096, "segment size in bytes")
	flag.Parse()

	w, err := wal.Open(wal.Options{DirPath: *dir, SegmentSize: *segment, SegmentFileExt: ".SEG", Sync: true})
	if err != nil {
		log.Fatalf("open wal: %v", err)
	}
	logserver.SyncDirs(*dir)
	log.Fatal(logserver.Serve(*addr, rosedbLog{w}, replay(w)))
}

func replay(w *wal.WAL) [][]byte {
	r := w.NewReader()
	var entries [][]byte
	for {
		data, _, err := r.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			log.Printf("replay stopped after %d entries: %v", len(entries), err)
			return entries
		}
		entries = append(entries, data)
	}
}
