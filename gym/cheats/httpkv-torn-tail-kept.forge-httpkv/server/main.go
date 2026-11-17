package main

import (
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/put", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("v" + r.URL.Query().Get("k")[1:]))
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
