package chain

import (
	"math/big"

	"github.com/ethereum/go-ethereum/crypto"
)

// UUIDToUint256 конвертирует произвольную строку (обычно UUID) в uint256 через
// keccak256. Детерминистично и совпадает с Solidity: uint256(keccak256(bytes(s))).
//
// Возвращает новый *big.Int — вызывающий может мутировать результат не затрагивая
// последующие вызовы.
func UUIDToUint256(s string) *big.Int {
	h := crypto.Keccak256([]byte(s))
	return new(big.Int).SetBytes(h)
}
