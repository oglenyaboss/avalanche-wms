package chain

import (
	"math/big"
	"testing"
)

// Известные векторы keccak256 получены через `cast keccak "<uuid>"`.
// Solidity: uint256(keccak256(bytes(uuid))) — big-endian, match Go big.Int.SetBytes.
var keccakVectors = []struct {
	name     string
	input    string
	expected string // hex без 0x
}{
	{
		name:     "example UUID v4",
		input:    "550e8400-e29b-41d4-a716-446655440000",
		expected: "2f779c94a35dceba72fe536ce28c5fea7566753044cdf9da29f6402ea964b7f9",
	},
	{
		name:     "empty string",
		input:    "",
		expected: "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
	},
	{
		name:     "simple ascii",
		input:    "abc",
		expected: "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45",
	},
}

func TestUUIDToUint256_KnownVectors(t *testing.T) {
	for _, tc := range keccakVectors {
		t.Run(tc.name, func(t *testing.T) {
			got := UUIDToUint256(tc.input)
			want, ok := new(big.Int).SetString(tc.expected, 16)
			if !ok {
				t.Fatalf("setup: can't parse hex %q", tc.expected)
			}
			if got.Cmp(want) != 0 {
				t.Errorf("keccak256(%q)\n  got:  %s\n  want: %s", tc.input, got.Text(16), want.Text(16))
			}
		})
	}
}

func TestUUIDToUint256_Deterministic(t *testing.T) {
	u := "550e8400-e29b-41d4-a716-446655440000"
	a := UUIDToUint256(u)
	b := UUIDToUint256(u)
	if a.Cmp(b) != 0 {
		t.Errorf("non-deterministic: %s vs %s", a.Text(16), b.Text(16))
	}
	// Проверяем что каждый вызов возвращает новый объект — вызывающий может
	// мутировать результат не боясь зацепить кешированный.
	a.Add(a, big.NewInt(1))
	c := UUIDToUint256(u)
	if c.Cmp(b) != 0 {
		t.Errorf("mutation of result affected subsequent call: %s != %s", c.Text(16), b.Text(16))
	}
}

func TestUUIDToUint256_DifferentInputsDifferentOutputs(t *testing.T) {
	a := UUIDToUint256("550e8400-e29b-41d4-a716-446655440000")
	b := UUIDToUint256("550e8400-e29b-41d4-a716-446655440001")
	if a.Cmp(b) == 0 {
		t.Errorf("collision on similar UUIDs: %s", a.Text(16))
	}
}

func TestUUIDToUint256_FitsIn256Bits(t *testing.T) {
	got := UUIDToUint256("anything")
	if got.BitLen() > 256 {
		t.Errorf("result overflows uint256: bitlen=%d", got.BitLen())
	}
	if got.Sign() < 0 {
		t.Errorf("result is negative (must be unsigned): %s", got.Text(16))
	}
}
