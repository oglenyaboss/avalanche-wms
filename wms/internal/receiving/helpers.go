package receiving

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"wms/internal/domain"
)

// buildCloseCargoplaceSummary compares the expected and received SKUs for a cargoplace and builds a summary including total counts and shortages by SKU.
func buildCloseCargoplaceSummary(expectedSKUs []ExpectedSKU, receivedSKUs []ReceivedSKUCount) CloseCargoplaceSummary {
	receivedBySKU := make(map[uuid.UUID]ReceivedSKUCount, len(receivedSKUs))
	totalReceived := 0
	for _, received := range receivedSKUs {
		receivedBySKU[received.SKUID] = received
		totalReceived += received.ReceivedQty
	}

	totalExpected := 0
	totalShortage := 0
	shortageBySKU := make([]ShortageBySKU, 0)
	for _, expected := range expectedSKUs {
		totalExpected += expected.ExpectedQty
		receivedQty := 0
		if received, ok := receivedBySKU[expected.SKUID]; ok {
			receivedQty = received.ReceivedQty
		}

		shortage := expected.ExpectedQty - receivedQty
		if shortage <= 0 {
			continue
		}

		totalShortage += shortage
		shortageBySKU = append(shortageBySKU, ShortageBySKU{
			SKUName:  expected.SKUName,
			Expected: expected.ExpectedQty,
			Received: receivedQty,
			Shortage: shortage,
		})
	}

	return CloseCargoplaceSummary{
		ProductsReceived: totalReceived,
		ProductsExpected: totalExpected,
		Shortage:         totalShortage,
		ShortageBySKU:    shortageBySKU,
	}
}

func isBufferBin(bin *domain.Bin) bool {
	if bin == nil || bin.Section == nil {
		return false
	}

	section := strings.ToUpper(strings.TrimSpace(*bin.Section))
	return section == "BUFFER"
}

// scanCodeMaxLen — верхняя граница длины TTN/QR/barcode. DB-колонки text не имеют ограничения,
// сервис обрезает аномальные входы до того, как попасть в SQL/Kafka.
const scanCodeMaxLen = 256

// validateScanCode нормализует и валидирует пользовательский ввод (TTN, штрих-код, QR).
// Возвращает обрезанную (TrimSpace) строку и ErrInvalidInput на пустую/слишком длинную.
// Полагаться только на DB unique constraint небезопасно: пробельные строки не отфильтровываются.
func validateScanCode(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrInvalidInput
	}
	if len(trimmed) > scanCodeMaxLen {
		return "", ErrInvalidInput
	}
	return trimmed, nil
}

func payloadHashForReceiving(productID, cargoplaceID uuid.UUID) (string, error) {
	payload := struct {
		ProductID     uuid.UUID `json:"product_id"`
		CargoplaceID  uuid.UUID `json:"cargoplace_id"`
		AggregateType string    `json:"aggregate_type"`
		EventType     string    `json:"event_type"`
	}{
		ProductID:     productID,
		CargoplaceID:  cargoplaceID,
		AggregateType: "receiving",
		EventType:     "wms.receiving.v1",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("receiving.payloadHashForReceiving marshal payload: %w", err)
	}

	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
