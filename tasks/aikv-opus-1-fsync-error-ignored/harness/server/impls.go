package main

import (
	opus1 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/opus-1"
)

var impls = map[string]func(string) (store, error){
	"opus-1": func(dir string) (store, error) {
		db, err := opus1.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
}
