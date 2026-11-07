package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
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
	http.HandleFunc("/put", s.putForm)
	http.HandleFunc("/get", s.getForm)
	http.HandleFunc("PUT /kv/{key}", s.putPath)
	http.HandleFunc("GET /kv/{key}", s.getPath)
	http.HandleFunc("DELETE /kv/{key}", s.deletePath)
	http.HandleFunc("GET /health", health)
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(int64(complete)); err != nil {
		return nil, err
	}
	if _, err := f.Seek(int64(complete), io.SeekStart); err != nil {
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
		line := string(data[complete : complete+i])
		if k, v, ok := strings.Cut(line, " "); ok {
			values[k] = v
		} else {
			delete(values, line)
		}
		complete += i + 1
	}
}

func record(key string, value *string) (string, bool) {
	if key == "" || strings.ContainsAny(key, " \n") {
		return "", false
	}
	if value == nil {
		return key + "\n", true
	}
	return key + " " + *value + "\n", !strings.Contains(*value, "\n")
}

func (s *store) putForm(w http.ResponseWriter, r *http.Request) {
	v := r.FormValue("v")
	s.apply(w, r.FormValue("k"), &v)
}

func (s *store) putPath(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v := string(body)
	s.apply(w, r.PathValue("key"), &v)
}

func (s *store) deletePath(w http.ResponseWriter, r *http.Request) {
	s.apply(w, r.PathValue("key"), nil)
}

func (s *store) apply(w http.ResponseWriter, k string, v *string) {
	line, ok := record(k, v)
	if !ok {
		http.Error(w, "keys cannot hold spaces or line breaks, values cannot hold line breaks", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.WriteString(s.file, line); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.persist(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if v == nil {
		delete(s.values, k)
	} else {
		s.values[k] = *v
	}
	fmt.Fprint(w, "OK")
}

func (s *store) getForm(w http.ResponseWriter, r *http.Request) {
	s.get(w, r, r.FormValue("k"))
}

func (s *store) getPath(w http.ResponseWriter, r *http.Request) {
	s.get(w, r, r.PathValue("key"))
}

func (s *store) get(w http.ResponseWriter, r *http.Request, k string) {
	s.mu.Lock()
	v, ok := s.values[k]
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

func health(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, "OK")
}
