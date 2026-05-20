package chain

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

func TestABI_Parses(t *testing.T) {
	if ContractABI == "" {
		t.Fatal("ContractABI is empty — embed did not fire")
	}
	parsed, err := abi.JSON(strings.NewReader(ContractABI))
	if err != nil {
		t.Fatalf("parse ABI: %v", err)
	}
	// Все batch-методы, которые использует адаптер, обязаны существовать.
	required := []string{"batchAccept", "batchPutAway", "batchPick", "batchShip"}
	for _, name := range required {
		if _, ok := parsed.Methods[name]; !ok {
			t.Errorf("ABI missing method %q", name)
		}
	}
	// И событие, которое мы ожидаем фиксить в onchain_events.
	if _, ok := parsed.Events["ItemTransition"]; !ok {
		t.Error("ABI missing event ItemTransition")
	}
}

func TestABI_AggregateMapping(t *testing.T) {
	for agg, method := range aggregateToMethod {
		parsed, err := abi.JSON(strings.NewReader(ContractABI))
		if err != nil {
			t.Fatalf("parse ABI: %v", err)
		}
		if _, ok := parsed.Methods[method]; !ok {
			t.Errorf("aggregateToMethod[%q] = %q — метод отсутствует в ABI", agg, method)
		}
	}
}
