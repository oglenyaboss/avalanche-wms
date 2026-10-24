import { publicApi, unwrap } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";

import { saveSession, type Session } from "./session";

export async function login(username: string, password: string): Promise<Session> {
  try {
    const tokens = await unwrap(publicApi.POST("/auth/login", { body: { username, password } }));
    return saveSession(tokens, username);
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      throw new ApiError(401, "INVALID_CREDENTIALS", "Неверный логин или пароль");
    }
    throw error;
  }
}
