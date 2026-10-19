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
	return entityDetailsHash(salt, cargo, operatorID, occurredAtMs)
}

// PutAwayDetailsHash is keccak256(abi.encode(bytes32 salt, bytes16 storageBinId,
// bytes16 operatorId, uint64 occurredAtMs)).
func PutAwayDetailsHash(salt [32]byte, storageBinID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var bin [32]byte
	copy(bin[:16], storageBinID[:])
	return entityDetailsHash(salt, bin, operatorID, occurredAtMs)
}

// PickDetailsHash is keccak256(abi.encode(bytes32 salt, bytes16 shippingBufferBinId,
// bytes16[] orderIds, bytes16 operatorId, uint64 occurredAtMs)).
func PickDetailsHash(salt [32]byte, shippingBufferBinID uuid.UUID, orderIDs []uuid.UUID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	return orderedListDetailsHash(salt, []uuid.UUID{shippingBufferBinID}, orderIDs, operatorID, occurredAtMs)
}

// ShipDetailsHash is keccak256(abi.encode(bytes32 salt, bytes16 dispatchId,
// bytes16 shippingBufferBinId, bytes16[] orderIds, bytes16 operatorId,
// uint64 occurredAtMs)).
func ShipDetailsHash(salt [32]byte, dispatchID, shippingBufferBinID uuid.UUID, orderIDs []uuid.UUID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	return orderedListDetailsHash(salt, []uuid.UUID{dispatchID, shippingBufferBinID}, orderIDs, operatorID, occurredAtMs)
}

// orderedListDetailsHash is the shared body of the order-list details hashes
// (pick, ship): keccak256(abi.encode(bytes32 salt, bytes16... entities,
// bytes16[] orderIds, bytes16 operatorId, uint64 occurredAtMs)). Head = salt,
// one word per entity, the array's offset word, operator, occurredAtMs; tail =
// length, one left-aligned bytes16 per orderId. orderIds go ascending by raw
// bytes without duplicates; no input is mutated.
func orderedListDetailsHash(salt [32]byte, entities []uuid.UUID, orderIDs []uuid.UUID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	orders := sortedUniqueOrderIDs(orderIDs)

	var operator [32]byte
	copy(operator[:16], operatorID[:])

	var ms, offset, length [32]byte
	binary.BigEndian.PutUint64(ms[24:], occurredAtMs)
	// заголовок из len(entities)+4 статических слов: соль, сущности, offset, оператор, время
	binary.BigEndian.PutUint64(offset[24:], uint64(32*(len(entities)+4)))
	binary.BigEndian.PutUint64(length[24:], uint64(len(orders)))

	buf := make([]byte, 0, 32*(len(entities)+5+len(orders)))
	buf = append(buf, salt[:]...)
	for _, entity := range entities {
		var word [32]byte
		copy(word[:16], entity[:])
		buf = append(buf, word[:]...)
	}
	buf = append(buf, offset[:]...)
	buf = append(buf, operator[:]...)
	buf = append(buf, ms[:]...)
	buf = append(buf, length[:]...)
	for _, id := range orders {
		var word [32]byte
		copy(word[:16], id[:])
		buf = append(buf, word[:]...)
	}
	return keccak256(buf)
}

// sortedUniqueOrderIDs returns a fresh slice of the input sorted ascending by
// raw bytes with duplicates removed; the input is not mutated.
func sortedUniqueOrderIDs(ids []uuid.UUID) []uuid.UUID {
	sorted := make([]uuid.UUID, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i][:], sorted[j][:]) < 0
	})
	out := sorted[:0]
	for i, id := range sorted {
		if i > 0 && id == sorted[i-1] {
			continue
		}
		out = append(out, id)
	}
	return out
}

// entityDetailsHash is the shared body of the single-entity details hashes
// (receive, putaway): keccak256(abi.encode(bytes32 salt, bytes16 entity,
// bytes16 operatorId, uint64 occurredAtMs)).
func entityDetailsHash(salt, entity [32]byte, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var operator [32]byte
	copy(operator[:16], operatorID[:])

	var ms [32]byte
	binary.BigEndian.PutUint64(ms[24:], occurredAtMs)

	return keccak256(salt[:], entity[:], operator[:], ms[:])
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
