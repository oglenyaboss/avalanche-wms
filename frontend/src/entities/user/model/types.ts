export type UserRole = 'ADMIN' | 'OPERATOR' | 'CUSTOMER' | 'AUDITOR'

export type User = {
  id: string
  role: UserRole
}
