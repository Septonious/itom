module itom.local/mailstream

go 1.27.1

require (
	github.com/emersion/go-imap/v2 v2.0.0-00010101000000-000000000000
	github.com/flynn/noise v1.1.0
)

require (
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519 // indirect
	golang.org/x/sys v0.5.0 // indirect
)

replace github.com/emersion/go-imap/v2 => ../source/core/third_party/go-imap
