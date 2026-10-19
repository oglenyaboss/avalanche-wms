package signedaction

import (
	"bytes"
	"encoding/binary"
	"sort"

	"github.com/google/uuid"
	"golang.org/x/crypto/sha3"
)

// TypeHash is keccak256("avalanche-wms/SignedAction/v1"), the first word of the challenge.
var TypeHash = keccak256([]byte("avalanche-wms/SignedAction/v1"))

// NoItems is keccak256 of the empty itemsHash, used by key actions.
var NoItems = keccak256(nil)

// Action types of the lifecycle in SignedAction.sol.
const (
	TypeReceive uint8 = 1
	TypePutAway uint8 = 2
	TypePick    uint8 = 3
	TypeShip    uint8 = 4

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

// ItemID is uint256(keccak256(bytes(uuid))), the canonical lowercase uuid string.
func ItemID(productID uuid.UUID) [32]byte {
	return keccak256([]byte(productID.String()))
}

// SortByItemID returns a new slice sorted ascending by ItemID; the input is not mutated.
func SortByItemID(ids []uuid.UUID) []uuid.UUID {
	type pair struct {
		product uuid.UUID
		item    [32]byte
	}
	pairs := make([]pair, len(ids))
	for i, id := range ids {
		pairs[i] = pair{id, ItemID(id)}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return bytes.Compare(pairs[i].item[:], pairs[j].item[:]) < 0
	})
	sorted := make([]uuid.UUID, len(ids))
	for i, p := range pairs {
		sorted[i] = p.product
	}
	return sorted
}

// ItemsHash is keccak256 of the concatenated ItemIDs in the given order,
// abi.encodePacked(uint256[] itemIds) with itemIds ascending.
func ItemsHash(sorted []uuid.UUID) [32]byte {
	buf := make([]byte, 0, 32*len(sorted))
	for _, id := range sorted {
		item := ItemID(id)
		buf = append(buf, item[:]...)
	}
	return keccak256(buf)
}

// ReceiveDetailsHash is keccak256(abi.encode(bytes32 salt, bytes16 cargoplaceId,
// bytes16 operatorId, uint64 occurredAtMs)).
func ReceiveDetailsHash(salt [32]byte, cargoplaceID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var cargo [32]byte
	copy(cargo[:16], cargoplaceID[:])

	var operator [32]byte
	copy(operator[:16], operatorID[:])

	var ms [32]byte
	binary.BigEndian.PutUint64(ms[24:], occurredAtMs)

	return keccak256(salt[:], cargo[:], operator[:], ms[:])
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
