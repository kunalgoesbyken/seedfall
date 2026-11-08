package main

import (
	haiku1 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/haiku-1"
	haiku2 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/haiku-2"
	haiku3 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/haiku-3"
	opus1 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/opus-1"
	opus2 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/opus-2"
	opus3 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/opus-3"
	sonnet1 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/sonnet-1"
	sonnet2 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/sonnet-2"
	sonnet3 "github.com/kunalgoesbyken/seedfall/examples/aikv/impl/sonnet-3"
)

var impls = map[string]func(string) (store, error){
	"haiku-1": func(dir string) (store, error) {
		db, err := haiku1.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"haiku-2": func(dir string) (store, error) {
		db, err := haiku2.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"haiku-3": func(dir string) (store, error) {
		db, err := haiku3.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"opus-1": func(dir string) (store, error) {
		db, err := opus1.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"opus-2": func(dir string) (store, error) {
		db, err := opus2.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"opus-3": func(dir string) (store, error) {
		db, err := opus3.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"sonnet-1": func(dir string) (store, error) {
		db, err := sonnet1.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"sonnet-2": func(dir string) (store, error) {
		db, err := sonnet2.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
	"sonnet-3": func(dir string) (store, error) {
		db, err := sonnet3.Open(dir)
		if err != nil {
			return nil, err
		}
		return db, nil
	},
}
