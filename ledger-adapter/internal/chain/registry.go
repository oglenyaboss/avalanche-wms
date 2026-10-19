package chain

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const (
	RoleNone     uint8 = 0
	RoleAdmin    uint8 = 1
	RoleOperator uint8 = 2
)

// RegistryKey — ключ из ConsortiumRegistry.
type RegistryKey struct {
	QX, QY [32]byte
	OrgID  [16]byte
	Role   uint8
	Active bool
}

// contractCaller — подмножество ethclient для eth_call.
type contractCaller interface {
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

// Registry — read-only обёртка над ConsortiumRegistry.
type Registry struct {
	eth  contractCaller
	addr common.Address
	abi  abi.ABI
}

// registryKeyTuple совпадает с Key из контракта: go-ethereum сопоставляет
// компоненты tuple по именам полей.
type registryKeyTuple struct {
	Qx     [32]byte
	Qy     [32]byte
	OrgId  [16]byte
	Role   uint8
	Active bool
}

func NewRegistry(eth contractCaller, addr common.Address) (*Registry, error) {
	parsed, err := abi.JSON(strings.NewReader(RegistryABI))
	if err != nil {
		return nil, err
	}
	return &Registry{eth: eth, addr: addr, abi: parsed}, nil
}

func (r *Registry) ABI() abi.ABI { return r.abi }

func (r *Registry) GetKey(ctx context.Context, keyID [32]byte) (RegistryKey, error) {
	data, err := r.abi.Pack("getKey", keyID)
	if err != nil {
		return RegistryKey{}, err
	}
	out, err := r.call(ctx, data)
	if err != nil {
		return RegistryKey{}, err
	}
	res, err := r.abi.Unpack("getKey", out)
	if err != nil {
		return RegistryKey{}, err
	}
	t := abi.ConvertType(res[0], new(registryKeyTuple)).(*registryKeyTuple)
	return RegistryKey{QX: t.Qx, QY: t.Qy, OrgID: t.OrgId, Role: t.Role, Active: t.Active}, nil
}

func (r *Registry) OrganizationExists(ctx context.Context, orgID [16]byte) (bool, error) {
	data, err := r.abi.Pack("organizationExists", orgID)
	if err != nil {
		return false, err
	}
	out, err := r.call(ctx, data)
	if err != nil {
		return false, err
	}
	res, err := r.abi.Unpack("organizationExists", out)
	if err != nil {
		return false, err
	}
	return res[0].(bool), nil
}

func (r *Registry) call(ctx context.Context, data []byte) ([]byte, error) {
	out, err := r.eth.CallContract(ctx, ethereum.CallMsg{To: &r.addr, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("registry: пустой ответ, по адресу нет контракта")
	}
	return out, nil
}
