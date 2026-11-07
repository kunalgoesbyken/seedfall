module github.com/kunalgoesbyken/seedfall/examples/etcdraft

go 1.26.0

replace github.com/kunalgoesbyken/seedfall => ../..

require (
	github.com/kunalgoesbyken/seedfall v0.0.0-20261107124130-8bb15248b417
	go.etcd.io/raft/v3 v3.7.0
	google.golang.org/protobuf v1.36.12
)
