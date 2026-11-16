package main

import (
	haiku1 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/haiku-1"
)

var impls = map[string]func(string) (store, error){
	"haiku-1": func(dir string) (store, error) {
		db, err := haiku1.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
}
