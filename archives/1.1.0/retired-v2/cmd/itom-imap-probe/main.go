// itom-imap-probe is an offline diagnostic, not a VPN/server replacement.
package main

import (
	"flag"
	"fmt"
	"itom.local/core/manager"
	"os"
)

func main() {
	state := flag.String("state", "/var/lib/itom", "existing private server state; stop itom.service first")
	account := flag.String("account", "", "account alias or email; default: first configured account")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	if err := manager.RunIMAPTransactions(*state, *account, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
