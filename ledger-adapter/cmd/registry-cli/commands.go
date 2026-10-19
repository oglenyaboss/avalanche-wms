package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

// orgTx — общая цепочка register-org / add-admin-key: чтение org.json →
// подключение к реестру → precheck'и → pack → sendOwnerTx. Команды различаются
// полярностью precheck'а существования организации, методом ABI и его
// аргументами, финальным сообщением.
type orgTx struct {
	command string
	// mustExist: true — организация уже должна быть зарегистрирована
	// (add-admin-key), false — должна отсутствовать (register-org).
	mustExist  bool
	method     string
	packArgs   func(o orgRequest) []any
	successMsg func(o orgRequest, hash common.Hash) string
}

var registerOrg = orgTx{
	command:   "register-org",
	mustExist: false,
	method:    "registerOrganization",
	packArgs: func(o orgRequest) []any {
		return []any{o.orgID, o.name, o.qx, o.qy}
	},
	successMsg: func(o orgRequest, hash common.Hash) string {
		return fmt.Sprintf("организация %s «%s» зарегистрирована, ключ админа %s, tx %s\n",
			uuid.UUID(o.orgID), o.name, o.keyID.Hex(), hash.Hex())
	},
}

var addAdminKey = orgTx{
	command:   "add-admin-key",
	mustExist: true,
	method:    "registerAdminKey",
	packArgs: func(o orgRequest) []any {
		return []any{o.orgID, o.qx, o.qy}
	},
	successMsg: func(o orgRequest, hash common.Hash) string {
		return fmt.Sprintf("ключ админа %s добавлен в организацию %s, tx %s\n",
			o.keyID.Hex(), uuid.UUID(o.orgID), hash.Hex())
	},
}

func (tx orgTx) run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet(tx.command, flag.ContinueOnError)
	file := fs.String("file", "", "путь к org.json")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", tx.command, err)
	}
	if *file == "" {
		return fmt.Errorf("%s: нужен --file", tx.command)
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read %s: %w", *file, err)
	}
	org, err := parseOrgFile(data)
	if err != nil {
		return fmt.Errorf("parse org.json: %w", err)
	}

	ec, addr, err := dialRegistry()
	if err != nil {
		return fmt.Errorf("dial registry: %w", err)
	}
	defer ec.Close()
	reg, err := chain.NewRegistry(ec, addr)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	exists, err := reg.OrganizationExists(ctx, org.orgID)
	if err != nil {
		return fmt.Errorf("check organization: %w", err)
	}
	if exists && !tx.mustExist {
		return fmt.Errorf("организация %s уже зарегистрирована", uuid.UUID(org.orgID))
	}
	if !exists && tx.mustExist {
		return fmt.Errorf("организация %s не зарегистрирована", uuid.UUID(org.orgID))
	}
	key, err := reg.GetKey(ctx, [32]byte(org.keyID))
	if err != nil {
		return fmt.Errorf("get key: %w", err)
	}
	if key.Role != chain.RoleNone {
		return fmt.Errorf("ключ %s уже зарегистрирован", org.keyID.Hex())
	}

	owner, err := ownerKey()
	if err != nil {
		return fmt.Errorf("load owner key: %w", err)
	}
	calldata, err := reg.ABI().Pack(tx.method, tx.packArgs(org)...)
	if err != nil {
		return fmt.Errorf("pack %s: %w", tx.method, err)
	}
	hash, err := sendOwnerTx(ctx, ec, owner, addr, calldata)
	if err != nil {
		return fmt.Errorf("send tx: %w", err)
	}
	fmt.Print(tx.successMsg(org, hash))
	return nil
}
