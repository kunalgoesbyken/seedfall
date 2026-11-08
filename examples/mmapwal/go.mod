module github.com/kunalgoesbyken/seedfall/examples/mmapwal

go 1.26.0

replace github.com/kunalgoesbyken/seedfall => ../..

require (
	github.com/blevesearch/mmap-go v1.0.4
	github.com/gofrs/flock v0.13.1
	github.com/kunalgoesbyken/seedfall v0.2.0
)

require golang.org/x/sys v0.47.0 // indirect
