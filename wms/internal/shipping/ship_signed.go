package shipping

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
	"wms/internal/signedaction"
	"wms/internal/signedops"
)

// operationStore persists multi-part signed warehouse operations.
type operationStore interface {
	InsertOperation(ctx context.Context, op *signedops.Operation) error
	Operation(ctx context.Context, operationID uuid.UUID) (*signedops.Operation, error)
}

// signerSource returns the caller's active passkey and the WebAuthn relying party.
type signerSource interface {
	ActiveSigner(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error)
	RelyingParty() (rpID string, origins []string)
}

// shipDetails are the draft inputs hashed into every part's detailsHash. The
// orderIds are not stored: they are read back from the products the parts move
// (ADR §4 — every details field must come from the domain tables the action changes).
type shipDetails struct {
	DispatchID          uuid.UUID `json:"dispatch_id"`
	ShippingBufferBinID uuid.UUID `json:"shipping_buffer_bin_id"`
	OccurredAtMs        int64     `json:"occurred_at_ms"`
}

// DraftShip builds the signed Ship operation for shipping a shipping-buffer bin's
// products onto a trip: the bin's READY_TO_SHIP products and the AT_GATE dispatch
// are checked read-only, every product's picking must already be confirmed on
// chain, and the set is split into parts of at most MaxItemsPerAction, each part
// waiting for its own passkey signature. Nothing is written except the draft.
func (s *Service) DraftShip(ctx context.Context, operatorID, bufferBinID, dispatchID uuid.UUID, productIDs []uuid.UUID) (*signedops.Operation, string, error) {
	if operatorID == uuid.Nil || bufferBinID == uuid.Nil || dispatchID == uuid.Nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", ErrInvalidInput)
	}

	bufferBin, err := s.repo.GetBinWithDestinationByID(ctx, bufferBinID)
	if err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip get bin: %w", err)
	}
	if bufferBin.Section == nil || *bufferBin.Section != binSectionShippingBuffer || bufferBin.DestinationID == nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", ErrBinNotShippingBuffer)
	}

	dispatch, err := s.repo.GetDispatchByID(ctx, dispatchID)
	if err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip get dispatch: %w", err)
	}
	if *bufferBin.DestinationID != dispatch.DestinationID {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", ErrDestinationMismatch)
	}
	if dispatch.Status != domain.OutboundDispatchStatusAtGate {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", ErrDispatchNotAtGate)
	}

	products, err := s.repo.ListProductsForShip(ctx, bufferBinID, productIDs)
	if err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip select products: %w", err)
	}
	if len(productIDs) == 0 && len(products) == 0 {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", ErrBufferEmpty)
	}
	if len(productIDs) > 0 && len(products) != len(productIDs) {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", ErrProductNotInBuffer)
	}

	items := make([]uuid.UUID, 0, len(products))
	orderByProduct := make(map[uuid.UUID]uuid.UUID, len(products))
	for _, p := range products {
		items = append(items, p.ProductID)
		if p.OrderID != nil {
			orderByProduct[p.ProductID] = *p.OrderID
		}
	}

	// Отгрузка разрешена, только когда сборка КАЖДОГО товара подтверждена в цепи (#15).
	unconfirmed, err := s.repo.UnconfirmedProducts(ctx, items, ledger.AggregatePicking)
	if err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip unconfirmed picking: %w", err)
	}
	if len(unconfirmed) > 0 {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip: %w", &PickingNotConfirmedError{ProductIDs: unconfirmed})
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, "", err
	}

	// Время обрезается до миллисекунд, иначе detailsHash не воспроизвести из БД (ADR §4).
	occurredAt := time.Now().UTC().Truncate(time.Millisecond)
	details, err := json.Marshal(shipDetails{
		DispatchID:          dispatchID,
		ShippingBufferBinID: bufferBinID,
		OccurredAtMs:        occurredAt.UnixMilli(),
	})
	if err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip marshal details: %w", err)
	}

	op, err := signedops.NewOperation(signedaction.TypeShip, operatorID, signer, details, items,
		func(salt [32]byte, partItems []uuid.UUID) [32]byte {
			orders := make([]uuid.UUID, 0, len(partItems))
			for _, id := range partItems {
				if orderID, ok := orderByProduct[id]; ok {
					orders = append(orders, orderID)
				}
			}
			return signedaction.ShipDetailsHash(salt, dispatchID, bufferBinID, orders, operatorID, uint64(occurredAt.UnixMilli()))
		})
	if err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip new operation: %w", err)
	}

	if err := s.ops.InsertOperation(ctx, op); err != nil {
		return nil, "", fmt.Errorf("shipping.Service.DraftShip insert: %w", err)
	}
	rpID, _ := s.signer.RelyingParty()
	return op, rpID, nil
}

// SubmitShip verifies the operator's signatures over every part and applies the
// whole operation in one repository transaction. Signature errors are returned
// as is: nothing is written before every part verifies.
func (s *Service) SubmitShip(ctx context.Context, operatorID, operationID uuid.UUID, assertions map[uuid.UUID]signedaction.Assertion) (*ShipResponse, error) {
	op, err := s.ops.Operation(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("shipping.Service.SubmitShip load: %w", err)
	}
	if op == nil || op.CreatedBy != operatorID || op.ActionType != signedaction.TypeShip {
		return nil, fmt.Errorf("shipping.Service.SubmitShip: %w", signedops.ErrOperationNotFound)
	}
	if op.Submitted {
		return nil, fmt.Errorf("shipping.Service.SubmitShip: %w", signedops.ErrOperationSubmitted)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signer.CredentialID, op.SignerCredentialID) {
		return nil, fmt.Errorf("shipping.Service.SubmitShip: %w", signedops.ErrSignerNotActive)
	}

	rpID, origins := s.signer.RelyingParty()
	payloads, err := signedops.Envelopes(op, signer, assertions, rpID, origins)
	if err != nil {
		return nil, err
	}

	var details shipDetails
	if err := json.Unmarshal(op.Details, &details); err != nil {
		return nil, fmt.Errorf("shipping.Service.SubmitShip decode details: %w", err)
	}
	occurredAt := time.UnixMilli(details.OccurredAtMs).UTC()

	items := make([]uuid.UUID, 0, len(op.Parts))
	for _, p := range op.Parts {
		items = append(items, p.ItemIDs...)
	}

	// Повторная проверка врат на подписанном наборе: между черновиком и submit
	// подтверждение сборки появиться не могло, а исчезнуть — могло.
	if unconfirmed, err := s.repo.UnconfirmedProducts(ctx, items, ledger.AggregatePicking); err != nil {
		return nil, fmt.Errorf("shipping.Service.SubmitShip unconfirmed picking: %w", err)
	} else if len(unconfirmed) > 0 {
		return nil, fmt.Errorf("shipping.Service.SubmitShip: %w", &PickingNotConfirmedError{ProductIDs: unconfirmed})
	}

	var resp ShipResponse
	err = s.repo.WithTx(ctx, func(repo shippingRepository) error {
		// 1. Блокируем и проверяем буфер отгрузки.
		bin, err := repo.GetBufferBinForUpdate(ctx, details.ShippingBufferBinID)
		if err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip get bin: %w", err)
		}
		if bin.Section == nil || *bin.Section != binSectionShippingBuffer || bin.DestinationID == nil {
			return fmt.Errorf("shipping.Service.SubmitShip: %w", ErrBinNotShippingBuffer)
		}

		// 2. Блокируем и проверяем рейс: машина должна быть у ворот, destination должен совпадать.
		dispatch, err := repo.GetDispatchForUpdate(ctx, details.DispatchID)
		if err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip get dispatch: %w", err)
		}
		if *bin.DestinationID != dispatch.DestinationID {
			return fmt.Errorf("shipping.Service.SubmitShip: %w", ErrDestinationMismatch)
		}
		if dispatch.Status != domain.OutboundDispatchStatusAtGate {
			return fmt.Errorf("shipping.Service.SubmitShip: %w", ErrDispatchNotAtGate)
		}

		// 3. ADR §7: сначала блокируем строки товаров FOR UPDATE, затем операцию
		//    и ключ подписанта.
		products, err := repo.LockProductsForShip(ctx, details.ShippingBufferBinID, items)
		if err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip lock products: %w", err)
		}
		// Состав черновика должен дожить до submit: каждый подписанный товар всё
		// ещё READY_TO_SHIP в этой ячейке буфера, иначе подпись устарела.
		if len(products) != len(items) {
			return fmt.Errorf("shipping.Service.SubmitShip: %w", ErrDraftStale)
		}

		lockedOp, err := repo.LockOperation(ctx, operationID, operatorID)
		if err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip lock operation: %w", err)
		}

		if err := repo.LockSigner(ctx, op.SignerCredentialID); err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip lock signer: %w", err)
		}

		// 4. Переводим товары в статус SHIPPED.
		if err := repo.BatchUpdateProductsShipped(ctx, items); err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip update products: %w", err)
		}

		// 5. Создаём записи shippings по каждой части: время из деталей действия
		//    и ссылка на само действие.
		for _, p := range lockedOp.Parts {
			events := make([]shippingEvent, 0, len(p.ItemIDs))
			for _, id := range p.ItemIDs {
				events = append(events, shippingEvent{EventID: uuid.New(), ProductID: id})
			}
			if err := repo.BatchInsertShippings(ctx, events, details.DispatchID, operatorID, occurredAt, p.ActionID); err != nil {
				return fmt.Errorf("shipping.Service.SubmitShip insert shippings of part %s: %w", p.ActionID, err)
			}
		}

		// 6. Двигаем статус заказа: SHIPPED, если все товары отгружены; иначе
		//    PARTIALLY_SHIPPED, чтобы частично отгруженный заказ не выглядел как ASSEMBLED
		//    и не застревал, если отгрузка пошла раньше перехода в ASSEMBLED (issue #48).
		orderSet := make(map[uuid.UUID]struct{}, len(products))
		for _, p := range products {
			if p.OrderID != nil {
				orderSet[*p.OrderID] = struct{}{}
			}
		}
		orderIDs := make([]uuid.UUID, 0, len(orderSet))
		for orderID := range orderSet {
			orderIDs = append(orderIDs, orderID)
		}
		ordersCompleted, ordersPartiallyShipped, err := repo.UpdateOrdersShippedConditional(ctx, orderIDs)
		if err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip update orders: %w", err)
		}

		// 7. Проверяем остаток в буфере; если буфер пуст, переводим рейс в DEPARTED.
		remaining, err := repo.CountReadyToShipProductsInBuffer(ctx, details.ShippingBufferBinID)
		if err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip count remaining: %w", err)
		}
		dispatchDeparted := false
		if remaining == 0 {
			if err := repo.UpdateDispatchDeparted(ctx, details.DispatchID); err != nil {
				return fmt.Errorf("shipping.Service.SubmitShip depart dispatch: %w", err)
			}
			dispatchDeparted = true
		}

		if err := repo.WriteActions(ctx, lockedOp, payloads); err != nil {
			return fmt.Errorf("shipping.Service.SubmitShip write actions: %w", err)
		}

		resp = ShipResponse{
			ProductsShipped:        len(items),
			OutboxEventsCreated:    len(lockedOp.Parts),
			OrdersCompleted:        ordersCompleted,
			OrdersPartiallyShipped: ordersPartiallyShipped,
			DispatchDeparted:       dispatchDeparted,
			BufferRemaining:        remaining,
			OperationID:            op.OperationID.String(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &resp, nil
}
