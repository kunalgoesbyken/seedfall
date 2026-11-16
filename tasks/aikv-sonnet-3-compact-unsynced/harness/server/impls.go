package main

import (
	sonnet3 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/sonnet-3"
)

var impls = map[string]func(string) (store, error){
	"sonnet-3": func(dir string) (store, error) {
		db, err := sonnet3.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
}
