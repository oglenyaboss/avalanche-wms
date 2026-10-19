package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

func list(ctx context.Context) error {
	ec, addr, err := dialRegistry()
	if err != nil {
		return fmt.Errorf("dial registry: %w", err)
	}
	defer ec.Close()
	reg, err := chain.NewRegistry(ec, addr)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	a := reg.ABI()

	logs, err := ec.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(0),
		Addresses: []common.Address{addr},
		Topics: [][]common.Hash{{
			a.Events["OrganizationRegistered"].ID,
			a.Events["KeyRegistered"].ID,
			a.Events["KeyRevoked"].ID,
		}},
	})
	if err != nil {
		return fmt.Errorf("filter logs: %w", err)
	}
	orgs, err := foldLogs(a, logs)
	if err != nil {
		return fmt.Errorf("fold logs: %w", err)
	}
	fmt.Print(render(orgs))
	return nil
}

type orgKey struct {
	keyID  common.Hash
	role   uint8
	active bool
}

type org struct {
	id   [16]byte
	name string
	keys []orgKey
}

// foldLogs сворачивает журнал событий реестра в список организаций: порядок
// появления, у каждой — ключи в порядке регистрации. Организация без события
// OrganizationRegistered сохраняется с пустым именем.
func foldLogs(a abi.ABI, logs []types.Log) ([]org, error) {
	var orgs []org
	orgIdx := map[[16]byte]int{}
	keyLoc := map[common.Hash][2]int{}
	ensureOrg := func(id [16]byte) int {
		i, ok := orgIdx[id]
		if !ok {
			orgs = append(orgs, org{id: id})
			i = len(orgs) - 1
			orgIdx[id] = i
		}
		return i
	}

	for _, l := range logs {
		if len(l.Topics) == 0 {
			return nil, errors.New("лог без topics")
		}
		switch l.Topics[0] {
		case a.Events["OrganizationRegistered"].ID:
			if len(l.Topics) < 2 {
				return nil, errors.New("OrganizationRegistered: нет orgId в topics")
			}
			var id [16]byte
			copy(id[:], l.Topics[1][:16])
			out, err := a.Unpack("OrganizationRegistered", l.Data)
			if err != nil {
				return nil, fmt.Errorf("OrganizationRegistered: %w", err)
			}
			orgs[ensureOrg(id)].name = out[0].(string)

		case a.Events["KeyRegistered"].ID:
			if len(l.Topics) < 3 {
				return nil, errors.New("KeyRegistered: нет keyId/orgId в topics")
			}
			var id [16]byte
			copy(id[:], l.Topics[2][:16])
			out, err := a.Unpack("KeyRegistered", l.Data)
			if err != nil {
				return nil, fmt.Errorf("KeyRegistered: %w", err)
			}
			i := ensureOrg(id)
			orgs[i].keys = append(orgs[i].keys, orgKey{keyID: l.Topics[1], role: out[0].(uint8), active: true})
			keyLoc[l.Topics[1]] = [2]int{i, len(orgs[i].keys) - 1}

		case a.Events["KeyRevoked"].ID:
			if len(l.Topics) < 2 {
				return nil, errors.New("KeyRevoked: нет keyId в topics")
			}
			loc, ok := keyLoc[l.Topics[1]]
			if !ok {
				return nil, fmt.Errorf("KeyRevoked для неизвестного ключа %s", l.Topics[1].Hex())
			}
			orgs[loc[0]].keys[loc[1]].active = false
		}
	}
	return orgs, nil
}

func render(orgs []org) string {
	if len(orgs) == 0 {
		return "организаций нет\n"
	}
	var b strings.Builder
	for _, o := range orgs {
		if o.name == "" {
			fmt.Fprintf(&b, "%s\n", uuid.UUID(o.id))
		} else {
			fmt.Fprintf(&b, "%s  %s\n", uuid.UUID(o.id), o.name)
		}
		for _, k := range o.keys {
			role := "operator"
			if k.role == chain.RoleAdmin {
				role = "admin"
			}
			state := "активен"
			if !k.active {
				state = "отозван"
			}
			fmt.Fprintf(&b, "  %s  %-10s%s\n", k.keyID.Hex(), role, state)
		}
	}
	return b.String()
}
