package domain

type OrderStatus string

const (
	OrderStatusNew              OrderStatus = "NEW"
	OrderStatusAllocated        OrderStatus = "ALLOCATED"
	OrderStatusAssembled        OrderStatus = "ASSEMBLED"
	OrderStatusPartiallyShipped OrderStatus = "PARTIALLY_SHIPPED"
	OrderStatusShipped          OrderStatus = "SHIPPED"
)
