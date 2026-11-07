module gostmankv

go 1.25.0

require (
	github.com/kunalgoesbyken/gostman v1.15.3
	github.com/kunalgoesbyken/seedfall v0.0.0
)

require (
	github.com/Azure/go-ntlmssp v0.1.1 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/bufbuild/protocompile v0.14.1 // indirect
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/dop251/goja v0.0.0-20260603125802-cfe4039cb6d7 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	golang.org/x/crypto v0.47.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	httpkv v0.0.0-00010101000000-000000000000 // indirect
	software.sslmate.com/src/go-pkcs12 v0.7.4 // indirect
)

replace (
	github.com/kunalgoesbyken/seedfall => ../..
	httpkv => ../httpkv
)

tool httpkv/server
