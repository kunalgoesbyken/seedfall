package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type entry struct{ key, value string }

var client = &http.Client{Timeout: time.Second}

func main() {
	base := flag.String("url", "http://server:8080", "server base URL")
	writes := flag.Int("writes", 100, "number of writes")
	flag.Parse()

	var acked []entry
	for i := range *writes {
		e := entry{fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)}
		if body, status, err := call(*base + "/put?" + url.Values{"k": {e.key}, "v": {e.value}}.Encode()); err == nil && status == http.StatusOK && body == "OK" {
			acked = append(acked, e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("%d of %d writes acknowledged, verifying\n", len(acked), *writes)
	sim.Sometimes(len(acked) < *writes, "a crash interrupted the workload")
	for _, e := range acked {
		body := callUntilServed(*base + "/get?" + url.Values{"k": {e.key}}.Encode())
		sim.Assert(body == e.value, fmt.Sprintf("acknowledged write lost: %s=%s, server now answers %q", e.key, e.value, body))
	}
	fmt.Println("all acknowledged writes survived")
}

func call(u string) (string, int, error) {
	resp, err := client.Get(u)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode, err
}

func callUntilServed(u string) string {
	for {
		body, status, err := call(u)
		if err == nil && status != http.StatusInternalServerError {
			return body
		}
		time.Sleep(50 * time.Millisecond)
	}
}
