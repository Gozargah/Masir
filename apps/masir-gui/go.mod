module github.com/Gozargah/Masir/apps/masir-gui

go 1.27.3

require (
	github.com/egoist/mygo v0.7.4
	github.com/Gozargah/Masir/libs/masir-core v0.0.0
	github.com/Gozargah/Masir/libs/masir-proto v0.0.0
	golang.org/x/mod v0.42.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
	howett.net/plist v1.0.1
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261026165753-2ffab32bce7e // indirect
)

replace (
	github.com/Gozargah/Masir/libs/masir-core => ../../libs/masir-core
	github.com/Gozargah/Masir/libs/masir-proto => ../../libs/masir-proto
)

tool github.com/egoist/mygo/cmd/mygo
