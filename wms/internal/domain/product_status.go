package domain

type ProductStatus string

const (
	ProductStatusReceived    ProductStatus = "RECEIVED"
	ProductStatusStored      ProductStatus = "STORED"
	ProductStatusAllocated   ProductStatus = "ALLOCATED"
	ProductStatusAssembled   ProductStatus = "ASSEMBLED"
	ProductStatusReadyToShip ProductStatus = "READY_TO_SHIP"
	ProductStatusShipped     ProductStatus = "SHIPPED"
)
