package signedaction

import (
	"github.com/google/uuid"
	"golang.org/x/crypto/sha3"
)

// TypeHash is keccak256("avalanche-wms/SignedAction/v1"), the first word of the challenge.
var TypeHash = keccak256([]byte("avalanche-wms/SignedAction/v1"))

// NoItems is keccak256 of the empty itemsHash, used by key actions.
var NoItems = keccak256(nil)

// Action types of the key lifecycle in SignedAction.sol.
const (
	TypeKeyRegister uint8 = 5
	TypeKeyRevoke   uint8 = 6
)

// Challenge is keccak256 of the five ABI words: typehash, bytes16 actionId,
// uint8 actionType, itemsHash, detailsHash.
func Challenge(actionID uuid.UUID, actionType uint8, itemsHash, detailsHash [32]byte) [32]byte {
	var id [32]byte
	copy(id[:16], actionID[:])

	var typ [32]byte
	typ[31] = actionType

	return keccak256(TypeHash[:], id[:], typ[:], itemsHash[:], detailsHash[:])
}

// KeyDetailsHash is keccak256(qx || qy), the details hash of key actions.
func KeyDetailsHash(qx, qy [32]byte) [32]byte {
	return keccak256(qx[:], qy[:])
}

// KeyID is keccak256(qx || qy), the identifier of a key in the on-chain registry.
func KeyID(qx, qy [32]byte) [32]byte {
	return KeyDetailsHash(qx, qy)
}

func keccak256(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}
