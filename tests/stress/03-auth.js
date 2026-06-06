/**
 * 03-auth.js — Auth endpoints load test
 *
 * Цель: оценить пропускную способность /auth/login и /auth/refresh.
 * Auth — потенциальное узкое место из-за bcrypt (cost factor влияет на latency).
 *
 * Запуск (только ramp-сценарий по умолчанию):
 *   k6 run tests/stress/03-auth.js
 *
 * Запуск конкретного сценария:
 *   k6 run --scenario login_stress tests/stress/03-auth.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL, THRESHOLDS_AUTH } from './lib/config.js';

const JSON_HEADERS = { 'Content-Type': 'application/json' };

export const options = {
  scenarios: {
    // Сценарий 1: нагрузка на /auth/login
    login_stress: {
      executor: 'ramping-vus',
      stages: [
        { duration: '15s', target: 5 },
        { duration: '1m',  target: 20 },
        { duration: '2m',  target: 50 },
        { duration: '1m',  target: 50 },
        { duration: '15s', target: 0 },
      ],
      exec: 'loginLoad',
    },
    // Сценарий 2: нагрузка на /auth/refresh (токен получен в setup)
    refresh_stress: {
      executor: 'constant-vus',
      vus: 30,
      duration: '3m',
      exec: 'refreshLoad',
    },
  },
  thresholds: {
    'http_req_duration{endpoint:login}': ['p(95)<1500', 'p(99)<3000'],
    'http_req_duration{endpoint:refresh}': ['p(95)<300', 'p(99)<600'],
    http_req_failed: ['rate<0.01'],
  },
};

// setup() проверяет доступность /auth/login до старта VU.
// refreshLoad получает токен самостоятельно — shared-данные не нужны.
export function setup() {
  const res = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ username: 'operator', password: 'operator' }),
    { headers: JSON_HEADERS },
  );
  if (res.status !== 200) {
    console.error(`setup: login failed with status ${res.status}: ${res.body}`);
  }
  return {};
}

// Нагрузка на login
export function loginLoad() {
  const res = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ username: 'operator', password: 'operator' }),
    { headers: JSON_HEADERS, tags: { endpoint: 'login' } },
  );
  check(res, {
    'login: 200': (r) => r.status === 200,
    'login: has access_token': (r) => {
      try { return !!JSON.parse(r.body).access_token; } catch (_) { return false; }
    },
  });
  // Небольшая пауза, имитирующая время между логином и первым действием оператора
  sleep(0.5);
}

// Нагрузка на refresh.
// Каждый VU получает собственный refresh-токен через login — нельзя делить
// один токен на 30 VU: первый VU его потребляет, остальные получают 401.
export function refreshLoad() {
  // Шаг 1: login → получаем свежую пару токенов
  const loginRes = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ username: 'operator', password: 'operator' }),
    { headers: JSON_HEADERS },
  );
  if (loginRes.status !== 200) {
    console.warn(`VU${__VU}: refreshLoad login failed (${loginRes.status})`);
    return;
  }
  let refreshToken = '';
  try { refreshToken = JSON.parse(loginRes.body).refresh_token || ''; } catch (_) { /* */ }
  if (!refreshToken) return;

  sleep(0.05);

  // Шаг 2: refresh → получаем новый access_token
  const res = http.post(
    `${BASE_URL}/auth/refresh`,
    JSON.stringify({ refresh_token: refreshToken }),
    { headers: JSON_HEADERS, tags: { endpoint: 'refresh' } },
  );
  check(res, {
    'refresh: 200': (r) => r.status === 200,
    'refresh: has access_token': (r) => {
      try { return !!JSON.parse(r.body).access_token; } catch (_) { return false; }
    },
  });
  sleep(0.2);
}

export default loginLoad;
