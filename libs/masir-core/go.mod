module github.com/Gozargah/Masir/libs/masir-core

go 1.27.3

require (
	github.com/Gozargah/Masir/libs/masir-proto v0.0.0
	github.com/Microsoft/go-winio v0.6.2
	golang.org/x/sys v0.49.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261026165753-2ffab32bce7e
	google.golang.org/grpc v1.85.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/Gozargah/Masir/libs/masir-proto => ../masir-proto
