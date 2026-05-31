import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL } from './config.js';

// Заголовки для запросов с токеном
export function authHeaders(token) {
  return {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
  };
}

// Заголовки для публичных запросов (auth/login, health)
export const publicHeaders = { 'Content-Type': 'application/json' };

// Логин оператора. Возвращает access_token или пустую строку при ошибке.
export function login(username, password) {
  const res = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ username: username || 'operator', password: password || 'operator' }),
    { headers: publicHeaders },
  );
  if (res.status !== 200) return '';
  try {
    return JSON.parse(res.body).access_token || '';
  } catch (_) {
    return '';
  }
}

// POST с JSON-телом. Возвращает response.
export function postJSON(path, body, token) {
  return http.post(
    `${BASE_URL}${path}`,
    JSON.stringify(body),
    { headers: token ? authHeaders(token) : publicHeaders },
  );
}

// GET с токеном. Возвращает response.
export function getJSON(path, token) {
  return http.get(
    `${BASE_URL}${path}`,
    { headers: token ? authHeaders(token) : publicHeaders },
  );
}

// Разворачивает WMS success-envelope. Возвращает data или null.
export function unwrapData(res) {
  try {
    const body = JSON.parse(res.body);
    if (body.success) return body.data;
  } catch (_) { /* ignore */ }
  return null;
}

// Проверяет стандартный успешный ответ WMS ({success:true}).
export function checkSuccess(res, tag) {
  return check(res, {
    [`${tag}: status 200`]: (r) => r.status === 200,
    [`${tag}: success=true`]: (r) => {
      try { return JSON.parse(r.body).success === true; } catch (_) { return false; }
    },
  });
}

// Формирует zero-padded номер для stress TTN/CP кодов.
export function pad(n, width) {
  return String(n).padStart(width, '0');
}
