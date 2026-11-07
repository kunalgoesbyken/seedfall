package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

type store struct {
	sync   bool
	mu     sync.Mutex
	file   *os.File
	values map[string]string
}

func main() {
	fsync := flag.Bool("fsync", true, "fsync before acknowledging a write")
	flag.Parse()
	s, err := open("data", "kv.log")
	if err != nil {
		log.Fatal(err)
	}
	s.sync = *fsync
	http.HandleFunc("/put", s.put)
	http.HandleFunc("/get", s.get)
	log.Printf("serving %d keys", len(s.values))
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func open(dir, name string) (*store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := dir + "/" + name
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	values, complete := replay(data)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(int64(complete)); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	for _, d := range []string{dir, "."} {
		if err := syncDir(d); err != nil {
			return nil, err
		}
	}
	return &store{file: f, values: values}, nil
}

func replay(data []byte) (map[string]string, int) {
	values := map[string]string{}
	complete := 0
	for {
		i := bytes.IndexByte(data[complete:], '\n')
		if i < 0 {
			return values, complete
		}
		k, v, ok := strings.Cut(string(data[complete:complete+i]), " ")
		if !ok {
			return values, complete
		}
		values[k] = v
		complete += i + 1
	}
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *store) put(w http.ResponseWriter, r *http.Request) {
	k, v := r.FormValue("k"), r.FormValue("v")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.file, "%s %s\n", k, v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.persist(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.values[k] = v
	fmt.Fprint(w, "OK")
}

func (s *store) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	v, ok := s.values[r.FormValue("k")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, v)
}

func (s *store) persist() error {
	if !s.sync {
		return nil
	}
	return s.file.Sync()
}
