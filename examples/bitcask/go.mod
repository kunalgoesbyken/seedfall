module github.com/kunalgoesbyken/seedfall/examples/bitcask

go 1.26.0

replace (
	github.com/gofrs/flock => ../flockshim
	github.com/kunalgoesbyken/seedfall => ../..
)

require (
	github.com/kunalgoesbyken/seedfall v0.0.0-20261107122010-a699794acef9
	go.mills.io/bitcask/v2 v2.2.0
)

require (
	github.com/abcum/lcp v0.0.0-20201209214815-7a3f3840be81 // indirect
	github.com/gofrs/flock v0.13.1 // indirect
	github.com/hashicorp/go-immutable-radix/v2 v2.0.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/mattetti/filebuffer v1.0.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/sys v0.44.0 // indirect
)
