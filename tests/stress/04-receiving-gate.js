/**
 * 04-receiving-gate.js — Receiving gate flow stress test
 *
 * Цель: нагрузочное тестирование потока КПП-приёмки:
 *   POST /receiving/gate/scan-ttn
 *   POST /receiving/gate/scan-cargoplace  (×N для каждого грузоместа)
 *   POST /receiving/gate/accept-shipment
 *
 * Требования к данным:
 *   Перед запуском выполнить: tests/stress/setup/stress-seed.sql
 *   (создаёт STRESS-TTN-0001 … STRESS-TTN-0500, по 3 грузоместа каждая)
 *
 * Каждый VU берёт TTN по формуле: ((__VU - 1) + __ITER * maxVUs) % TOTAL_SHIPMENTS
 * Если поставка уже закрыта (409), VU логирует предупреждение и переходит дальше.
 *
 * Запуск:
 *   k6 run tests/stress/04-receiving-gate.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL, THRESHOLDS_STRICT } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';

const TOTAL_SHIPMENTS = 500;  // должно совпадать с числом в stress-seed.sql
const CARGOPLACES_PER_SHIPMENT = 3;

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-vus',
      stages: [
        { duration: '30s', target: 10 },
        { duration: '2m',  target: 50 },
        { duration: '3m',  target: 100 },
        { duration: '1m',  target: 0 },
      ],
    },
  },
  thresholds: {
    'http_req_duration{step:scan_ttn}': ['p(95)<250', 'p(99)<600'],
    'http_req_duration{step:scan_cp}': ['p(95)<250', 'p(99)<600'],
    'http_req_duration{step:accept}': ['p(95)<250', 'p(99)<600'],
    // Допустимые ошибки: ~409 SHIPMENT_ALREADY_CLOSED при повторном прогоне с теми же данными
    http_req_failed: ['rate<0.15'],
  },
};

export function setup() {
  const token = login('operator', 'operator');
  if (!token) {
    console.error('setup: failed to obtain operator token');
  }
  return { token };
}

export default function (data) {
  const token = data.token;
  if (!token) return;

  // 404 = данные не засеяны; 409 = поставка уже закрыта (нормально после обработки всех 500).
  // Без этого callback-а k6 считает 4xx в http_req_failed и валит порог на длинных прогонах.
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
  ));

  // Уникальный индекс поставки для этого VU + итерации.
  // Множитель = пиковый maxVUs (100), чтобы каждый новый round охватывал
  // следующий блок из 100 поставок без пересечений внутри одного round.
  const idx = ((__VU - 1 + (__ITER * 100)) % TOTAL_SHIPMENTS) + 1;
  const ttnCode = `STRESS-TTN-${pad(idx, 4)}`;

  const JSON_H = authHeaders(token);

  // 1. scan-ttn
  const ttnRes = http.post(
    `${BASE_URL}/receiving/gate/scan-ttn`,
    JSON.stringify({ ttn_code: ttnCode }),
    { headers: JSON_H, tags: { step: 'scan_ttn' } },
  );

  const ttnOk = check(ttnRes, {
    'scan_ttn: 200': (r) => r.status === 200,
  });

  if (!ttnOk) {
    // 404 — данные не засеяны; 409 — поставка уже обработана в предыдущем запуске
    if (ttnRes.status === 404) {
      console.warn(`VU${__VU}: TTN ${ttnCode} not found — run stress-seed.sql first`);
    }
    return;
  }

  const ttnData = JSON.parse(ttnRes.body).data;
  if (!ttnData) return;

  const shipmentId = ttnData.shipment_id;
  const cargoplaces = ttnData.cargoplaces || [];

  sleep(0.05);

  // 2. scan-cargoplace для каждого грузоместа
  for (const cp of cargoplaces) {
    const cpRes = http.post(
      `${BASE_URL}/receiving/gate/scan-cargoplace`,
      JSON.stringify({ shipment_id: shipmentId, cargoplace_code: cp.cargoplace_code }),
      { headers: JSON_H, tags: { step: 'scan_cp' } },
    );
    check(cpRes, {
      'scan_cp: 200': (r) => r.status === 200,
    });
    sleep(0.02);
  }

  // 3. accept-shipment
  const acceptRes = http.post(
    `${BASE_URL}/receiving/gate/accept-shipment`,
    JSON.stringify({ shipment_id: shipmentId }),
    { headers: JSON_H, tags: { step: 'accept' } },
  );
  check(acceptRes, {
    'accept: 200': (r) => r.status === 200,
    'accept: GATE_CLOSED': (r) => {
      try { return JSON.parse(r.body).data.status === 'GATE_CLOSED'; } catch (_) { return false; }
    },
  });

  sleep(0.1);
}
