module github.com/kunalgoesbyken/seedfall/examples/nutsdb

go 1.26.0

replace (
	github.com/gofrs/flock => ../flockshim
	github.com/kunalgoesbyken/seedfall => ../..
)

require (
	github.com/kunalgoesbyken/seedfall v0.0.0-20261107122010-a699794acef9
	github.com/nutsdb/nutsdb v1.1.0
)

require (
	github.com/antlabs/stl v0.0.2 // indirect
	github.com/antlabs/timer v0.1.4 // indirect
	github.com/bwmarrin/snowflake v0.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/edsrzf/mmap-go v1.2.0 // indirect
	github.com/gofrs/flock v0.13.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.7.1 // indirect
	github.com/tidwall/btree v1.6.0 // indirect
	github.com/xujiajun/utils v0.0.0-20220904132955-5f7c5b914235 // indirect
	golang.org/x/sys v0.44.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
