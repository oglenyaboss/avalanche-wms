import type { Schema } from "@/lib/api/client";

export type Role = Schema<"UserRole">;

export const roleLabels: Record<Role, string> = {
  ADMIN: "Администратор",
  OPERATOR: "Оператор склада",
  CUSTOMER: "Клиент",
  AUDITOR: "Аудитор",
};
