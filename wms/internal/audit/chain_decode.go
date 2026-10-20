package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"wms/internal/signedaction"
)

// ErrActionNotInTx is wrapped by Auth when the transaction does not contain the action.
var ErrActionNotInTx = errors.New("action not in transaction")

// OnChainAuth is the applyActions entry of one action, decoded from the transaction calldata.
type OnChainAuth struct {
	ItemIDs     []*big.Int
	DetailsHash [32]byte
	KeyID       [32]byte
	ActionType  uint8
	WebAuthn    signedaction.WebAuthn
}

// actionCall mirrors SignedMappingWMS.Action: go-ethereum maps tuple components to fields by name.
type actionCall struct {
	ActionId    [16]byte
	ActionType  uint8
	ItemIds     []*big.Int
	DetailsHash [32]byte
	KeyId       [32]byte
	Auth        authCall
}

// authCall mirrors WebAuthn.WebAuthnAuth the same way.
type authCall struct {
	R, S              [32]byte
	ChallengeIndex    *big.Int
	TypeIndex         *big.Int
	AuthenticatorData []byte
	ClientDataJSON    string
}

// Auth decodes the applyActions calldata of the transaction and returns the entry of the action.
func (c *Chain) Auth(ctx context.Context, tx common.Hash, actionID uuid.UUID) (OnChainAuth, error) {
	t, _, err := c.client.TransactionByHash(ctx, tx)
	if err != nil {
		return OnChainAuth{}, fmt.Errorf("audit.Chain.Auth: %w", err)
	}
	auth, err := decodeApplyActions(t.Data(), actionID)
	if err != nil {
		return OnChainAuth{}, fmt.Errorf("audit.Chain.Auth: %w", err)
	}
	return auth, nil
}

func decodeApplyActions(data []byte, actionID uuid.UUID) (OnChainAuth, error) {
	method := wmsABI.Methods["applyActions"]
	if len(data) < 4 || !bytes.Equal(data[:4], method.ID) {
		return OnChainAuth{}, errors.New("audit.decodeApplyActions: calldata is not applyActions")
	}
	vals, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		return OnChainAuth{}, fmt.Errorf("audit.decodeApplyActions unpack: %w", err)
	}
	if len(vals) != 1 {
		return OnChainAuth{}, errors.New("audit.decodeApplyActions: unexpected unpack shape")
	}
	entries := abi.ConvertType(vals[0], new([]actionCall)).(*[]actionCall)
	for _, call := range *entries {
		if uuid.UUID(call.ActionId) != actionID {
			continue
		}
		return OnChainAuth{
			ItemIDs:     call.ItemIds,
			DetailsHash: call.DetailsHash,
			KeyID:       call.KeyId,
			ActionType:  call.ActionType,
			WebAuthn: signedaction.WebAuthn{
				AuthenticatorData: call.Auth.AuthenticatorData,
				ClientDataJSON:    call.Auth.ClientDataJSON,
				ChallengeIndex:    int(call.Auth.ChallengeIndex.Int64()),
				TypeIndex:         int(call.Auth.TypeIndex.Int64()),
				R:                 call.Auth.R,
				S:                 call.Auth.S,
			},
		}, nil
	}
	return OnChainAuth{}, fmt.Errorf("audit.decodeApplyActions: %w", ErrActionNotInTx)
}
