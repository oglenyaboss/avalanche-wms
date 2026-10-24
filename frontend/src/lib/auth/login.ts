import { apiUrl } from "./auth-fetch";
import { saveSession, type Session, type Tokens } from "./session";

export async function login(username: string, password: string): Promise<Session> {
  const response = await fetch(apiUrl("/auth/login"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (response.status === 401) throw new Error("Неверный логин или пароль");
  if (!response.ok) throw new Error("Сервер не ответил, попробуйте ещё раз");
  return saveSession((await response.json()) as Tokens, username);
}
