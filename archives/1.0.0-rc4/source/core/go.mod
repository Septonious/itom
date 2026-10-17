module itom.local/core

go 1.27.1

require (
	github.com/emersion/go-imap/v2 v2.0.0-beta.8
	github.com/flynn/noise v1.1.0
	go.etcd.io/bbolt v1.4.3
	golang.org/x/term v0.46.0
)

require (
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/emersion/go-imap/v2 => ./third_party/go-imap
