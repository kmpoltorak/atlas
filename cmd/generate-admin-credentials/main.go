// Command generate-admin-credentials is a one-time local setup helper: it prints a bcrypt hash
// of a password read from stdin, suitable for ADMIN_PASSWORD_HASH (see docker-compose.yml,
// internal/config/config.go). Atlas itself never runs this — it's a developer/operator setup
// tool, not part of the running application, and produces no side effects.
//
// 2FA is deliberately not handled here: it's enabled from the dashboard after logging in
// (Account → Set up 2FA), via a real QR-code enrollment flow against a durable, mutable
// database setting (see internal/application/auth, migrations/008) — not a static secret
// generated once at the command line.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	fmt.Fprint(os.Stderr, "Admin password: ")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "read password:", err)
		os.Exit(1)
	}
	password = strings.TrimRight(password, "\r\n")
	if password == "" {
		fmt.Fprintln(os.Stderr, "password must not be empty")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}
	fmt.Println("ADMIN_PASSWORD_HASH=" + string(hash))
}
