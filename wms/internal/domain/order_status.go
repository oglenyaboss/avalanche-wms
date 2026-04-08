package domain

type OrderStatus string

const (
	OrderStatusNew                OrderStatus = "NEW"
	OrderStatusAllocated          OrderStatus = "ALLOCATED"
	OrderStatusAssemblyInProgress OrderStatus = "ASSEMBLY_IN_PROGRESS"
	OrderStatusAssembled          OrderStatus = "ASSEMBLED"
	OrderStatusReadyToShip        OrderStatus = "READY_TO_SHIP"
	OrderStatusShipped            OrderStatus = "SHIPPED"
)
