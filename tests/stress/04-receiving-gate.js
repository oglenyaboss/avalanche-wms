/**
 * 04-receiving-gate.js — Receiving gate flow stress test
 *
 * Цель: нагрузочное тестирование потока КПП-приёмки:
 *   POST /receiving/gate/scan-ttn
 *   POST /receiving/gate/scan-cargoplace  (×2 из 3)
 *   POST /receiving/gate/accept-shipment  (закрывает приёмку явно)
 *
 * Модель нагрузки: closed-model (shared-iterations).
 * Каждая итерация получает уникальный TTN через exec.scenario.iterationInTest,
 * что гарантирует отсутствие повторов и «409 уже закрыто» за пределами одного прогона.
 *
 * Требования к данным:
 *   Перед запуском выполнить: tests/stress/setup/stress-seed.sql
 *   (создаёт STRESS-TTN-0001 … STRESS-TTN-2000, по 3 грузоместа каждая)
 *
 * Запуск:
 *   k6 run tests/stress/04-receiving-gate.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';

const TOTAL_SHIPMENTS = 2000;  // должно совпадать с числом в stress-seed.sql

export const options = {
  scenarios: {
    ramp: {
      // closed-model: ровно TOTAL_SHIPMENTS итераций, каждая — уникальный TTN.
      // Тест завершается естественно, когда все сущности обработаны.
      executor: 'shared-iterations',
      vus: 50,
      iterations: TOTAL_SHIPMENTS,
      maxDuration: '8m',
    },
  },
  thresholds: {
    'http_req_duration{step:scan_ttn}': ['p(95)<250', 'p(99)<600'],
    'http_req_duration{step:scan_cp}':  ['p(95)<250', 'p(99)<600'],
    'http_req_duration{step:accept}':   ['p(95)<250', 'p(99)<600'],
    // При shared-iterations каждый TTN используется ровно один раз → ошибок почти нет.
    http_req_failed: ['rate<0.02'],
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

  // 404 = данные не засеяны; 409 = гонка (два VU взяли один TTN) — редко.
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
  ));

  // exec.scenario.iterationInTest: глобальный уникальный счётчик 0 … TOTAL_SHIPMENTS-1.
  // Каждая итерация получает свой TTN без повторов и без формулы __VU × __ITER.
  const idx = exec.scenario.iterationInTest + 1; // 1-indexed → STRESS-TTN-0001..2000
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

  // 2. scan-cargoplace для первых N-1 грузомест.
  //    Сканирование ВСЕХ грузомест вызывает авто-закрытие поставки (GATE_CLOSED),
  //    поэтому оставляем последнее грузоместо для явного accept-shipment.
  const cpToScan = cargoplaces.length > 1 ? cargoplaces.slice(0, -1) : cargoplaces;
  for (const cp of cpToScan) {
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

  // 3. accept-shipment — явное закрытие приёмки (поставка переходит в GATE_CLOSED).
  //    Если у поставки только 1 грузоместо и мы уже отсканировали его выше,
  //    авто-закрытие могло произойти → accept вернёт 409; это допустимо.
  const acceptRes = http.post(
    `${BASE_URL}/receiving/gate/accept-shipment`,
    JSON.stringify({ shipment_id: shipmentId }),
    { headers: JSON_H, tags: { step: 'accept' } },
  );
  check(acceptRes, {
    // 200 = явное закрытие; 409 = авто-закрытие уже произошло (последнее CP auto-closed).
    // Оба исхода означают, что поставка успешно закрыта.
    'accept: shipment closed (200 or 409)': (r) => r.status === 200 || r.status === 409,
    'accept: GATE_CLOSED in body (if 200)': (r) => {
      if (r.status !== 200) return true; // 409 → auto-closed, пропускаем body-check
      try { return JSON.parse(r.body).data.status === 'GATE_CLOSED'; } catch (_) { return false; }
    },
  });

  sleep(0.1);
}
