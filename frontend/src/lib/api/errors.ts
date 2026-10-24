export class ApiError extends Error {
  override readonly name = "ApiError";

  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

const plainTextErrors: Record<string, [code: string, message: string]> = {
  "invalid request body": ["INVALID_REQUEST", "Некорректный запрос"],
  unauthorized: ["UNAUTHORIZED", "Сессия истекла, войдите заново"],
  forbidden: ["FORBIDDEN", "Недостаточно прав для этого действия"],
  "user already exists": ["USER_ALREADY_EXISTS", "Пользователь уже существует"],
  "internal server error": ["INTERNAL_ERROR", "Внутренняя ошибка сервера"],
};

export function toApiError(status: number, body: unknown): ApiError {
  if (isErrorEnvelope(body)) return new ApiError(status, body.error.code, body.error.message);
  const known = typeof body === "string" ? plainTextErrors[body.trim()] : undefined;
  if (known) return new ApiError(status, known[0], known[1]);
  return new ApiError(status, `HTTP_${status}`, `Сервер ответил ошибкой ${status}`);
}

export const networkError = () => new ApiError(0, "NETWORK_ERROR", "Нет связи с сервером");

function isErrorEnvelope(body: unknown): body is { error: { code: string; message: string } } {
  if (typeof body !== "object" || body === null || !("error" in body)) return false;
  const error = body.error as { code?: unknown; message?: unknown } | null;
  return typeof error?.code === "string" && typeof error.message === "string";
}
