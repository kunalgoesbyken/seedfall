module github.com/kunalgoesbyken/seedfall/examples/diskv

go 1.26.0

replace github.com/kunalgoesbyken/seedfall => ../..

require (
	github.com/kunalgoesbyken/seedfall v0.0.0-20261107122010-a699794acef9
	github.com/peterbourgon/diskv/v3 v3.0.1
)

require github.com/google/btree v1.0.0 // indirect
