module github.com/kunalgoesbyken/seedfall/examples/bboltkv

go 1.26.0

replace github.com/kunalgoesbyken/seedfall => ../..

require (
	github.com/kunalgoesbyken/seedfall v0.2.0
	go.etcd.io/bbolt v1.5.0
)

require golang.org/x/sys v0.45.0 // indirect
