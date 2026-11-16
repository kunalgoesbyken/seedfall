package main

import (
	sonnet1 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/sonnet-1"
)

var impls = map[string]func(string) (store, error){
	"sonnet-1": func(dir string) (store, error) {
		db, err := sonnet1.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
}
