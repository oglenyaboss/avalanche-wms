export type UserRole = 'ADMIN' | 'OPERATOR' | 'CUSTOMER'

export type User = {
  id: string
  role: UserRole
}
