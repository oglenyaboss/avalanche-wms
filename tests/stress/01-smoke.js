/**
 * 01-smoke.js — Smoke test
 *
 * Цель: убедиться, что все ключевые эндпоинты WMS отвечают корректно
 * при минимальной нагрузке (1 VU, 1 итерация).
 *
 * Запуск:
 *   k6 run tests/stress/01-smoke.js
 * Или с кастомным URL:
 *   WMS_URL=http://localhost:8081 k6 run tests/stress/01-smoke.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL, THRESHOLDS_SMOKE } from './lib/config.js';
import { login, postJSON, getJSON, authHeaders, checkSuccess } from './lib/helpers.js';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: THRESHOLDS_SMOKE,
};

export default function () {
  // 404 ожидаемы для несуществующих ресурсов; 503 — при деградации WMS /health.
  // Без setResponseCallback k6 считает их в http_req_failed, что роняет порог.
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    503,
  ));

  // 1. Health check
  const healthRes = http.get(`${BASE_URL}/health`);
  check(healthRes, {
    'health: 200 or 503': (r) => r.status === 200 || r.status === 503,
    'health: status=ok or degraded': (r) => {
      try {
        const b = JSON.parse(r.body);
        return b.status === 'ok' || b.status === 'degraded';
      } catch (_) { return false; }
    },
  });
  sleep(0.1);

  // 2. Auth: login как operator
  const token = login('operator', 'operator');
  check({ token }, { 'auth: non-empty access_token': (v) => v.token.length > 0 });
  if (!token) return;
  sleep(0.1);

  // 3. Auth: login как admin (проверяем доступность admin-пользователя)
  const adminToken = login('admin', 'admin');
  check({ adminToken }, { 'auth: admin token obtained': (v) => v.adminToken.length > 0 });
  sleep(0.1);

  // 4. Auth: refresh-token
  const loginRes = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ username: 'operator', password: 'operator' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  const refreshToken = JSON.parse(loginRes.body).refresh_token || '';
  if (refreshToken) {
    const refreshRes = http.post(
      `${BASE_URL}/auth/refresh`,
      JSON.stringify({ refresh_token: refreshToken }),
      { headers: { 'Content-Type': 'application/json' } },
    );
    check(refreshRes, {
      'auth: refresh 200': (r) => r.status === 200,
      'auth: refresh returns new access_token': (r) => {
        try { return !!JSON.parse(r.body).access_token; } catch (_) { return false; }
      },
    });
  }
  sleep(0.1);

  // 5. GET /assembly/tasks — возвращает пустой список, если данных нет
  //    (destination_id — несуществующий UUID, ожидаем 404 или пустой список)
  const tasksRes = getJSON('/assembly/tasks?destination_id=00000000-0000-0000-0000-000000000001', token);
  check(tasksRes, {
    'assembly/tasks: responds (200 or 404)': (r) => r.status === 200 || r.status === 404,
  });
  sleep(0.1);

  // 6. Receiving gate: scan несуществующей TTN → ожидаем 404 TTN_NOT_FOUND
  const ttnRes = postJSON(
    '/receiving/gate/scan-ttn',
    { ttn_code: 'SMOKE-NONEXISTENT-TTN' },
    token,
  );
  check(ttnRes, {
    'receiving/gate/scan-ttn: 404 on unknown TTN': (r) => r.status === 404,
    'receiving/gate/scan-ttn: TTN_NOT_FOUND code': (r) => {
      try { return JSON.parse(r.body).error.code === 'TTN_NOT_FOUND'; } catch (_) { return false; }
    },
  });
  sleep(0.1);

  // 7. Putaway: scan несуществующего буфера → ожидаем 404 BIN_NOT_FOUND
  const putawayBufRes = postJSON(
    '/putaway/scan-buffer',
    { buffer_bin_id: '00000000-0000-0000-0000-000000000002' },
    token,
  );
  check(putawayBufRes, {
    'putaway/scan-buffer: 404 on unknown bin': (r) => r.status === 404,
  });
  sleep(0.1);

  // 8. Shipping/orders — GET не описан в handler, используем /shipping/scan-buffer
  //    с несуществующим bin_id → 404
  const shipBufRes = postJSON(
    '/shipping/scan-buffer',
    { buffer_bin_id: '00000000-0000-0000-0000-000000000003' },
    token,
  );
  check(shipBufRes, {
    'shipping/scan-buffer: 404 on unknown bin': (r) => r.status === 404,
  });
}
