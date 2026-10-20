package audit

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
)

func TestActionTopic(t *testing.T) {
	id, err := uuid.Parse("01929c5e-6f00-7000-8000-000000001001")
	if err != nil {
		t.Fatalf("parse uuid: %v", err)
	}
	want := common.HexToHash("0x01929c5e6f007000800000000000100100000000000000000000000000000000")
	if got := actionTopic(id); got != want {
		t.Fatalf("actionTopic = %s, want %s", got, want)
	}
}

func TestDecodeApplyActions(t *testing.T) {
	first := [16]byte(mustUUID(t, "01929c5e-6f00-7000-8000-000000001001"))
	second := [16]byte(mustUUID(t, "01929c5e-6f00-7000-8000-000000001002"))
	actions := []actionCall{
		{
			ActionId:    first,
			ActionType:  1,
			ItemIds:     []*big.Int{big.NewInt(101)},
			DetailsHash: [32]byte{0xaa},
			KeyId:       [32]byte{0xbb},
			Auth: authCall{
				R: [32]byte{0x01}, S: [32]byte{0x02},
				ChallengeIndex:    big.NewInt(23),
				TypeIndex:         big.NewInt(1),
				AuthenticatorData: []byte("authenticatorData"),
				ClientDataJSON:    `{"type":"webauthn.get"}`,
			},
		},
		{
			ActionId:    second,
			ActionType:  4,
			ItemIds:     []*big.Int{big.NewInt(201), big.NewInt(202)},
			DetailsHash: [32]byte{0xcc},
			KeyId:       [32]byte{0xdd},
			Auth: authCall{
				R: [32]byte{0x11}, S: [32]byte{0x12},
				ChallengeIndex:    big.NewInt(40),
				TypeIndex:         big.NewInt(2),
				AuthenticatorData: []byte("auth2"),
				ClientDataJSON:    `{"type":"webauthn.get","challenge":"x"}`,
			},
		},
	}
	data, err := wmsABI.Pack("applyActions", actions)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	got, err := decodeApplyActions(data, uuid.UUID(second))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ActionType != 4 || got.DetailsHash != [32]byte{0xcc} || got.KeyID != [32]byte{0xdd} {
		t.Errorf("decoded scalars = %+v", got)
	}
	if len(got.ItemIDs) != 2 || got.ItemIDs[0].Cmp(big.NewInt(201)) != 0 || got.ItemIDs[1].Cmp(big.NewInt(202)) != 0 {
		t.Errorf("decoded itemIds = %v", got.ItemIDs)
	}
	wa := got.WebAuthn
	if wa.ChallengeIndex != 40 || wa.TypeIndex != 2 || wa.R != [32]byte{0x11} || wa.S != [32]byte{0x12} {
		t.Errorf("decoded webauthn indexes/r/s = %+v", wa)
	}
	if string(wa.AuthenticatorData) != "auth2" || wa.ClientDataJSON != `{"type":"webauthn.get","challenge":"x"}` {
		t.Errorf("decoded webauthn data = %q/%q", wa.AuthenticatorData, wa.ClientDataJSON)
	}

	missing := mustUUID(t, "01929c5e-6f00-7000-8000-000000001099")
	if _, err := decodeApplyActions(data, missing); !errors.Is(err, ErrActionNotInTx) {
		t.Fatalf("missing id: err = %v, want ErrActionNotInTx", err)
	}
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %s: %v", s, err)
	}
	return id
}
