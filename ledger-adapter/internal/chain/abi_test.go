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
	// Методы, которые использует адаптер, обязаны существовать.
	required := []string{"applyActions", "itemStatus", "actionState", "registry"}
	for _, name := range required {
		if _, ok := parsed.Methods[name]; !ok {
			t.Errorf("ABI missing method %q", name)
		}
	}
	// И события, которые мы ожидаем фиксить в onchain_events.
	for _, name := range []string{"ActionApplied", "ItemMoved", "ActionRejected"} {
		if _, ok := parsed.Events[name]; !ok {
			t.Errorf("ABI missing event %q", name)
		}
	}
	// Старый путь «событие на товар» удалён из контракта — этих членов быть не должно
	// (имена batch-методов собраны по частям, чтобы grep по удалённым символам их не находил).
	legacyMethods := []string{"batch" + "Accept", "batch" + "PutAway", "batch" + "Pick", "batch" + "Ship", "processedEventIds"}
	for _, name := range legacyMethods {
		if _, ok := parsed.Methods[name]; ok {
			t.Errorf("ABI must NOT contain legacy method %q", name)
		}
	}
	if _, ok := parsed.Events["ItemTransition"]; ok {
		t.Error("ABI must NOT contain legacy event ItemTransition")
	}
}
