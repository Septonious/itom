module itom.local/qualification

go 1.27.1

require (
	github.com/emersion/go-imap/v2 v2.0.0-beta.8
	itom.local/core v0.0.0
)

require (
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/flynn/noise v1.1.0 // indirect
	go.etcd.io/bbolt v1.4.3 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	itom.local/mailstream v0.0.0 // indirect
)

replace github.com/emersion/go-imap/v2 => ../source/core/third_party/go-imap

replace itom.local/core => ../source/core

replace itom.local/mailstream => ../source/mailstream
