package consumer

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"ledger-adapter/internal/chain"
)

// aggregateSignedAction — aggregate_type подписанных действий (контракт §2/§3):
// Kafka value = JSON-строка с конвертом ActionEnvelope, key = actionId.
const aggregateSignedAction = "signed_action"

// Значения SignedAction.Type из ADR-0001: складские (1–4) и ключевые (5–6) действия.
const (
	actionTypeReceive     uint8 = 1
	actionTypePutAway     uint8 = 2
	actionTypePick        uint8 = 3
	actionTypeShip        uint8 = 4
	actionTypeKeyRegister uint8 = 5
	actionTypeKeyRevoke   uint8 = 6
)

// ActionEnvelope — конверт подписанного действия из outbox payload (контракт §2).
// hex-поля 0x-prefixed lowercase; keyId — ключ ПОДПИСАНТА (админа).
type ActionEnvelope struct {
	Version    int      `json:"version"`
	ActionID   string   `json:"actionId"`
	ActionType uint8    `json:"actionType"`
	ItemIDs    []string `json:"itemIds"`
	// DetailsHash — только у складских действий (ADR §4); у ключевых пустой.
	DetailsHash string `json:"detailsHash"`
	SubjectKey  *struct {
		QX string `json:"qx"`
		QY string `json:"qy"`
	} `json:"subjectKey"`
	KeyID    string `json:"keyId"`
	WebAuthn struct {
		AuthenticatorData string `json:"authenticatorData"`
		ClientDataJSON    string `json:"clientDataJSON"`
		ChallengeIndex    int64  `json:"challengeIndex"`
		TypeIndex         int64  `json:"typeIndex"`
		R                 string `json:"r"`
		S                 string `json:"s"`
	} `json:"webauthn"`
}

// Message — распарсенное kafka-сообщение с идентификаторами event/product и
// типом агрегата (из Kafka header aggregate_type, проставленного Debezium
// EventRouter). KafkaMsg храним полностью для commit-of-offset и DLQ-republish.
// Для подписанных действий задано ровно одно из Action (ключевое) /
// WarehouseAction (складское); для них ProductID = uuid.Nil
// (key = actionId, не product_id).
//
// Передавать только через pointer (gocritic hugeParam: kafka.Message ~152 байт).
type Message struct {
	EventID         uuid.UUID
	ProductID       uuid.UUID
	AggregateType   string // "receiving"/"putaway"/"picking"/"shipping"/"signed_action"
	Action          *chain.KeyAction
	WarehouseAction *chain.WarehouseAction
	Topic           string
	KafkaMsg        kafka.Message
}

// validAggregates derived from fsmOrder — single source of truth for known types.
var validAggregates = func() map[string]bool {
	m := make(map[string]bool, len(fsmOrder)+1)
	for _, a := range fsmOrder {
		m[a] = true
	}
	m[aggregateSignedAction] = true
	return m
}()

// Parse извлекает EventID из header "id", ProductID из m.Key (Debezium outbox
// aggregate_id), AggregateType из header "aggregate_type" (добавлен через
// transforms.outbox.table.fields.additional.placement). Для signed_action key
// обязан совпадать с id (это сам actionId), а value (JSON-строка с конвертом)
// раскладывается в chain.KeyAction. Невалидное сообщение — error; вызывающий
// должен коммитнуть offset и отправить в DLQ.
func Parse(m *kafka.Message) (*Message, error) {
	eventIDStr := headerValue(m.Headers, "id")
	if eventIDStr == "" {
		return nil, fmt.Errorf("missing 'id' header (expected event_id UUID)")
	}
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse event_id %q: %w", eventIDStr, err)
	}

	agg := headerValue(m.Headers, "aggregate_type")
	if agg == "" {
		return nil, fmt.Errorf("missing 'aggregate_type' header")
	}
	if !validAggregates[agg] {
		return nil, fmt.Errorf("unknown aggregate_type: %s", agg)
	}

	if agg == aggregateSignedAction {
		return parseSignedAction(m, eventID, eventIDStr)
	}

	if len(m.Key) == 0 {
		return nil, fmt.Errorf("missing kafka key (expected product_id UUID)")
	}
	productID, err := uuid.Parse(string(m.Key))
	if err != nil {
		return nil, fmt.Errorf("parse product_id %q: %w", string(m.Key), err)
	}

	return &Message{
		EventID:       eventID,
		ProductID:     productID,
		AggregateType: agg,
		Topic:         m.Topic,
		KafkaMsg:      *m,
	}, nil
}

// parseSignedAction разбирает подписанное действие (контракт §2): value —
// JSON-строка (Debezium отдаёт payload-колонку как строку), внутри которой
// конверт; key = id header = actionId.
func parseSignedAction(m *kafka.Message, eventID uuid.UUID, eventIDStr string) (*Message, error) {
	if string(m.Key) != eventIDStr {
		return nil, fmt.Errorf("signed_action: key %q must equal id header %q", string(m.Key), eventIDStr)
	}
	var raw string
	if err := json.Unmarshal(m.Value, &raw); err != nil {
		return nil, fmt.Errorf("signed_action: value must be a JSON string with the envelope: %w", err)
	}
	var env ActionEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("signed_action: decode envelope: %w", err)
	}
	if env.Version != 1 {
		return nil, fmt.Errorf("signed_action: unsupported version %d", env.Version)
	}
	if env.ActionID != eventIDStr {
		return nil, fmt.Errorf("signed_action: actionId %q != id header %q", env.ActionID, eventIDStr)
	}
	switch {
	case env.ActionType == actionTypeKeyRegister || env.ActionType == actionTypeKeyRevoke:
		if env.SubjectKey == nil {
			return nil, fmt.Errorf("signed_action: missing subjectKey")
		}
		action, err := env.toKeyAction(eventID)
		if err != nil {
			return nil, fmt.Errorf("signed_action: %w", err)
		}
		return &Message{
			EventID:       eventID,
			AggregateType: aggregateSignedAction,
			Action:        action,
			Topic:         m.Topic,
			KafkaMsg:      *m,
		}, nil
	case env.ActionType >= actionTypeReceive && env.ActionType <= actionTypeShip:
		action, err := env.toWarehouseAction(eventID)
		if err != nil {
			return nil, fmt.Errorf("signed_action: %w", err)
		}
		return &Message{
			EventID:         eventID,
			AggregateType:   aggregateSignedAction,
			WarehouseAction: action,
			Topic:           m.Topic,
			KafkaMsg:        *m,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported action type %d", env.ActionType)
	}
}

// toKeyAction переводит конверт ключевого действия в chain.KeyAction.
// ActionId — сырые 16 байт UUID (ADR §2); Qx/Qy — регистрируемый ключ.
func (env *ActionEnvelope) toKeyAction(id uuid.UUID) (*chain.KeyAction, error) {
	qx, err := decodeHex32("subjectKey.qx", env.SubjectKey.QX)
	if err != nil {
		return nil, err
	}
	qy, err := decodeHex32("subjectKey.qy", env.SubjectKey.QY)
	if err != nil {
		return nil, err
	}
	keyID, auth, err := env.authOf()
	if err != nil {
		return nil, err
	}
	return &chain.KeyAction{
		ActionId:    [16]byte(id),
		ActionType:  env.ActionType,
		Qx:          qx,
		Qy:          qy,
		SignerKeyId: keyID,
		Auth:        auth,
	}, nil
}

// toWarehouseAction переводит конверт складского действия в
// chain.WarehouseAction. ItemIds — UUID товаров в порядке конверта (ADR §9:
// порядок проставляет WMS, адаптер не меняет).
func (env *ActionEnvelope) toWarehouseAction(id uuid.UUID) (*chain.WarehouseAction, error) {
	detailsHash, err := decodeHex32("detailsHash", env.DetailsHash)
	if err != nil {
		return nil, err
	}
	keyID, auth, err := env.authOf()
	if err != nil {
		return nil, err
	}
	itemIds := make([]*big.Int, len(env.ItemIDs))
	for i, s := range env.ItemIDs {
		itemID, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("itemIds[%d]: %w", i, err)
		}
		itemIds[i] = chain.UUIDToUint256(itemID.String())
	}
	return &chain.WarehouseAction{
		ActionId:    [16]byte(id),
		ActionType:  env.ActionType,
		ItemIds:     itemIds,
		DetailsHash: detailsHash,
		KeyId:       keyID,
		Auth:        auth,
	}, nil
}

// authOf декодирует общие для всех действий поля подписи: keyId ПОДПИСАНТА и
// WebAuthnAuth. ClientDataJSON — строка как есть; индексы — *big.Int.
func (env *ActionEnvelope) authOf() ([32]byte, chain.WebAuthnAuth, error) {
	keyID, err := decodeHex32("keyId", env.KeyID)
	if err != nil {
		return [32]byte{}, chain.WebAuthnAuth{}, err
	}
	r, err := decodeHex32("webauthn.r", env.WebAuthn.R)
	if err != nil {
		return [32]byte{}, chain.WebAuthnAuth{}, err
	}
	s, err := decodeHex32("webauthn.s", env.WebAuthn.S)
	if err != nil {
		return [32]byte{}, chain.WebAuthnAuth{}, err
	}
	authData, err := decodeHex("webauthn.authenticatorData", env.WebAuthn.AuthenticatorData)
	if err != nil {
		return [32]byte{}, chain.WebAuthnAuth{}, err
	}
	return keyID, chain.WebAuthnAuth{
		R:                 r,
		S:                 s,
		ChallengeIndex:    big.NewInt(env.WebAuthn.ChallengeIndex),
		TypeIndex:         big.NewInt(env.WebAuthn.TypeIndex),
		AuthenticatorData: authData,
		ClientDataJSON:    env.WebAuthn.ClientDataJSON,
	}, nil
}

// decodeHex32 — 0x-prefixed hex ровно в 32 байта (bytes32).
func decodeHex32(field, s string) ([32]byte, error) {
	b, err := decodeHex(field, s)
	if err != nil {
		return [32]byte{}, err
	}
	if len(b) != 32 {
		return [32]byte{}, fmt.Errorf("%s: want 32 bytes, got %d", field, len(b))
	}
	var out [32]byte
	copy(out[:], b)
	return out, nil
}

// decodeHex — 0x-prefixed hex-строка в байты.
func decodeHex(field, s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") {
		return nil, fmt.Errorf("%s: not 0x-prefixed", field)
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("%s: bad hex: %w", field, err)
	}
	return b, nil
}

func headerValue(headers []kafka.Header, key string) string {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
