export type Role = "ADMIN" | "OPERATOR" | "CUSTOMER" | "AUDITOR";

export const roleLabels: Record<Role, string> = {
  ADMIN: "Администратор",
  OPERATOR: "Оператор склада",
  CUSTOMER: "Клиент",
  AUDITOR: "Аудитор",
};
