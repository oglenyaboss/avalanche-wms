package putaway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

var (
	ErrBufferBinNotFound  = errors.New("BUFFER_BIN_NOT_FOUND")
	ErrStorageBinNotFound = errors.New("STORAGE_BIN_NOT_FOUND")
	ErrProductNotFound    = errors.New("PRODUCT_NOT_FOUND")
	ErrProductNotInBuffer = errors.New("PRODUCT_NOT_IN_BUFFER")
	ErrProductNotReceived = errors.New("PRODUCT_NOT_RECEIVED")
	ErrInvalidInput       = errors.New("INVALID_INPUT")
)

type Service struct {
	repo  putawayRepository
	carts map[string][]uuid.UUID
	mu    sync.RWMutex
}

type putawayRepository interface {
	WithTx(ctx context.Context, fn func(putawayRepository) error) error
	GetBufferBinByID(ctx context.Context, bufferBinID uuid.UUID) (*domain.Bin, error)
	GetStorageBinByID(ctx context.Context, StorageBinID uuid.UUID) (*domain.Bin, error)
	ListProductsByBufferBin(ctx context.Context, bufferBinID uuid.UUID) ([]*ProductBufferItem, error)
	GetProductsByIDForUpdate(ctx context.Context, productID uuid.UUID) (*domain.Product, error)
	GetSKUByProductID(ctx context.Context, productID uuid.UUID) (*domain.SKU, error)
	UpdateProductStorage(ctx context.Context, productID, binID uuid.UUID) error
	InsertPutaway(ctx context.Context, params *InsertPutawayParams) error
	InsertOutboxEvents(ctx context.Context, params *OutboxEventsParams) error
}

func NewService(repo putawayRepository) *Service {
	return &Service{
		repo:  repo,
		carts: make(map[string][]uuid.UUID),
	}
}

func (s *Service) GetBufferProducts(ctx context.Context, operatorID, bufferBinID uuid.UUID) (*ScanBufferResponse, error) {
	if operatorID == uuid.Nil || bufferBinID == uuid.Nil {
		return nil, fmt.Errorf("putaway.Service.GetBufferProducts: %w", ErrInvalidInput)
	}

	bufferBin, err := s.repo.GetBufferBinByID(ctx, bufferBinID)
	if err != nil {
		return nil, fmt.Errorf("putaway.Service.GetBufferProducts: %w", err)
	}

	products, err := s.repo.ListProductsByBufferBin(ctx, bufferBinID)
	if err != nil {
		return nil, fmt.Errorf("putaway.Service.GetBufferProducts list products: %w", err)
	}

	respProducts := make([]ProductBufferItemResponse, 0, len(products))
	for _, p := range products {
		respProducts = append(respProducts, ProductBufferItemResponse{
			ProductID: p.ProductID.String(),
			SKUName:   p.SKUName,
			QRCode:    p.QRCode,
			Status:    p.Status,
		})
	}

	return &ScanBufferResponse{
		BufferBinId:   bufferBin.BinID.String(),
		BufferCode:    bufferBin.Code,
		Products:      respProducts,
		TotalProducts: len(respProducts),
	}, nil
}

func (s *Service) AddToPutawayCart(ctx context.Context, operatorID, productID, bufferBinID uuid.UUID) (*ScanProductResponse, error) {
	if operatorID == uuid.Nil || bufferBinID == uuid.Nil || productID == uuid.Nil {
		return nil, fmt.Errorf("putaway.Service.AddToPutawayCart: %w", ErrInvalidInput)
	}

	var product *domain.Product
	var sku *domain.SKU

	err := s.repo.WithTx(ctx, func(txRepo putawayRepository) error {
		var err error
		product, err = txRepo.GetProductsByIDForUpdate(ctx, productID)
		if err != nil {
			return err
		}

		if product.BinID == nil || *product.BinID != bufferBinID {
			return ErrProductNotInBuffer
		}

		if product.Status != "RECEIVED" {
			return ErrProductNotReceived
		}

		sku, err = txRepo.GetSKUByProductID(ctx, productID)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	key := operatorID.String()
	s.mu.Lock()
	s.carts[key] = append(s.carts[key], productID)
	cartSize := len(s.carts[key])
	s.mu.Unlock()

	return &ScanProductResponse{
		ProductID: product.ProductID.String(),
		SKUName:   sku.Name,
		QRCode:    product.QRCode,
		CartSize:  cartSize,
	}, nil
}

func (s *Service) PlaceProductsToStorageBin(ctx context.Context, operatorID uuid.UUID, productsIDs []uuid.UUID, storageBinID uuid.UUID) (*ScanStorageBinResponse, error) {
	if operatorID == uuid.Nil || storageBinID == uuid.Nil || len(productsIDs) == 0 {
		return nil, fmt.Errorf("putaway.Service.PlaceProductsToStorageBin: %w", ErrInvalidInput)
	}

	var storageBin *domain.Bin
	placedCount := 0
	occurredAt := time.Now().UTC()

	err := s.repo.WithTx(ctx, func(txRepo putawayRepository) error {
		var err error
		storageBin, err = txRepo.GetStorageBinByID(ctx, storageBinID)
		if err != nil {
			return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin: %w", err)
		}

		for _, productID := range productsIDs {
			if err := txRepo.UpdateProductStorage(ctx, productID, storageBinID); err != nil {
				return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin update product %s: %w", productID, err)
			}

			if err := txRepo.InsertPutaway(ctx, &InsertPutawayParams{
				EventID:       uuid.New(),
				ProductID:     productID,
				BinID:         storageBinID,
				OperatorID:    operatorID,
				OnChainStatus: "PENDING_ONCHAIN",
				OccurredAt:    occurredAt,
			}); err != nil {
				return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin insert putaway: %w", err)
			}

			placedCount++
		}

		if err := txRepo.InsertOutboxEvents(ctx, &OutboxEventsParams{
			ProductIDs:   productsIDs,
			StorageBinID: storageBinID,
		}); err != nil {
			return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin insert outbox events: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	key := operatorID.String()
	s.mu.Lock()
	delete(s.carts, key)
	s.mu.Unlock()

	return &ScanStorageBinResponse{
		StorageBinID:        storageBin.BinID.String(),
		StorageBinCode:      storageBin.Code,
		ProductsPlaced:      placedCount,
		OutboxEventsCreated: placedCount,
	}, nil
}
