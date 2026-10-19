package putaway

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedops"
)

type Service struct {
	repo   putawayRepository
	ops    operationStore
	signer signerSource
}

type putawayRepository interface {
	WithTx(ctx context.Context, fn func(putawayRepository) error) error
	GetBufferBinByID(ctx context.Context, bufferBinID uuid.UUID) (*domain.Bin, error)
	GetStorageBinByID(ctx context.Context, StorageBinID uuid.UUID) (*domain.Bin, error)
	ListProductsByBufferBin(ctx context.Context, bufferBinID uuid.UUID) ([]*ProductBufferItem, error)
	GetProductByID(ctx context.Context, productID uuid.UUID) (*domain.Product, error)
	GetProductsByIDForUpdate(ctx context.Context, productID uuid.UUID) (*domain.Product, error)
	GetSKUByProductID(ctx context.Context, productID uuid.UUID) (*domain.SKU, error)
	UpdateProductStorage(ctx context.Context, productID, binID uuid.UUID) error
	InsertPutaway(ctx context.Context, params *InsertPutawayParams) error
	LockOperation(ctx context.Context, operationID, createdBy uuid.UUID) (*signedops.Operation, error)
	LockSigner(ctx context.Context, credentialID []byte) error
	WriteActions(ctx context.Context, op *signedops.Operation, payloads [][]byte) error
	CheckChainStatus(ctx context.Context, productIDs []uuid.UUID, aggregateType string) error
	CountReceivedInBuffer(ctx context.Context, bufferBinID uuid.UUID) (int, error)
}

func NewService(repo putawayRepository, ops operationStore, signer signerSource) *Service {
	return &Service{repo: repo, ops: ops, signer: signer}
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
		BufferBinID:   bufferBin.BinID.String(),
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

	if _, err := s.repo.GetBufferBinByID(ctx, bufferBinID); err != nil {
		if errors.Is(err, ErrBufferBinNotFound) {
			return nil, fmt.Errorf("putaway.Service.AddToPutawayCart: %w", ErrBufferBinNotFound)
		}
		return nil, fmt.Errorf("putaway.Service.AddToPutawayCart verify buffer: %w", err)
	}

	product, err := s.repo.GetProductByID(ctx, productID)
	if err != nil {
		return nil, err
	}

	if product.BinID == nil || *product.BinID != bufferBinID {
		return nil, ErrProductNotInBuffer
	}

	if product.Status != "RECEIVED" {
		return nil, ErrProductNotReceived
	}

	sku, err = s.repo.GetSKUByProductID(ctx, productID)
	if err != nil {
		return nil, err
	}

	// #46: cart size is DERIVED from DB (count of RECEIVED products in this buffer bin),
	// not an in-memory map, so it survives a WMS restart and horizontal scaling. It reflects
	// everything staged in the bin, not just this operator's scans — the frontend holds the
	// authoritative product_ids[] it sends to ScanStorageBin.
	cartSize, err := s.repo.CountReceivedInBuffer(ctx, bufferBinID)
	if err != nil {
		return nil, fmt.Errorf("putaway.Service.AddToPutawayCart cart size: %w", err)
	}

	return &ScanProductResponse{
		ProductID: product.ProductID.String(),
		SKUName:   sku.Name,
		QRCode:    product.QRCode,
		CartSize:  cartSize,
	}, nil
}
