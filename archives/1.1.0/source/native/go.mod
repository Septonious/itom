module itom.local/mobile

go 1.27.1

require (
	github.com/xjasonlyu/tun2socks/v2 v2.7.0
	go.etcd.io/bbolt v1.4.3
	golang.org/x/mobile v0.0.0-20260908204917-8b95e45f8d3e
	golang.org/x/net v0.59.0
	gvisor.dev/gvisor v0.0.0-20260701204157-69c2d17aea96
	itom.local/core v0.0.0
)

require (
	github.com/emersion/go-imap/v2 v2.0.0-beta.8 // indirect
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/flynn/noise v1.1.0 // indirect
	github.com/google/btree v1.1.3 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/exp v0.0.0-20260611194520-c48552f49976 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
)

replace itom.local/core => ../core

replace github.com/emersion/go-imap/v2 => ../core/third_party/go-imap
