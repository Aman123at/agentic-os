module github.com/amantiwari/agentic-os

go 1.27.1

require (
	connectrpc.com/connect v1.21.0
	github.com/creack/pty v1.1.24
	github.com/landlock-lsm/go-landlock v0.10.1
	github.com/spf13/cobra v1.10.2
	golang.org/x/sys v0.48.0
	google.golang.org/protobuf v1.36.12
	modernc.org/sqlite v1.58.0
	mvdan.cc/sh/v3 v3.14.1
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	kernel.org/pub/linux/libs/security/libcap/psx v1.2.77 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)
