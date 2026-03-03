package domain

type UserRole string

const (
	UserRoleAdmin    UserRole = "ADMIN"
	UserRoleOperator UserRole = "OPERATOR"
	UserRoleCustomer UserRole = "CUSTOMER"
)
