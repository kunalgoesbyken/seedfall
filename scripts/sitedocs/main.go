package main

import (
	"fmt"
	"os"
	"sort"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sitedocs <repo root>")
		os.Exit(2)
	}
	problems, err := run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "sitedocs:", err)
		os.Exit(1)
	}
	if len(problems) == 0 {
		return
	}
	sort.Strings(problems)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "sitedocs:", p)
	}
	os.Exit(1)
}
