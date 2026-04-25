package domain

type OrderStatus string

const (
	OrderStatusNew       OrderStatus = "NEW"
	OrderStatusAllocated OrderStatus = "ALLOCATED"
	OrderStatusAssembled OrderStatus = "ASSEMBLED"
	OrderStatusShipped   OrderStatus = "SHIPPED"
)
