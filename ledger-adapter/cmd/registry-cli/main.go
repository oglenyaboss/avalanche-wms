// registry-cli — CLI владельца консорциума: регистрация организаций в
// ConsortiumRegistry и просмотр реестра. Собирается в образ ledger-adapter
// (бинарь /registry-cli), запуск — цель registry-cli в Makefile.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const usage = `usage:
  registry-cli register-org --file <org.json>
  registry-cli add-admin-key --file <org.json>
  registry-cli revoke-key --key-id <0x…>
  registry-cli list

окружение: RPC_URL, REGISTRY_ADDR (или <NAME>_FILE); OWNER_KEY (или OWNER_KEY_FILE) — для register-org, add-admin-key и revoke-key
`

var errUsage = errors.New("usage")

func main() {
	err := run(os.Args[1:])
	if errors.Is(err, errUsage) {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-cli: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch args[0] {
	case "register-org":
		return registerOrg.run(ctx, args[1:])
	case "add-admin-key":
		return addAdminKey.run(ctx, args[1:])
	case "revoke-key":
		return revokeKey(ctx, args[1:])
	case "list":
		return list(ctx)
	default:
		return errUsage
	}
}
