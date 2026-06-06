/**
 * 05-receiving-table.js — Receiving table flow stress test
 *
 * Цель: нагрузочное тестирование потока приёмки на столе:
 *   POST /receiving/table/scan-cargoplace
 *   POST /receiving/table/scan-box
 *   POST /receiving/table/scan-sku
 *   POST /receiving/table/scan-qr
 *   POST /receiving/table/close-box
 *   POST /receiving/table/scan-buffer
 *   POST /receiving/table/close-cargoplace  ← создаёт outbox event (→ Kafka → блокчейн)
 *
 * Модель нагрузки: closed-model (shared-iterations).
 * Каждая итерация получает уникальный UUID грузоместа через exec.scenario.iterationInTest,
 * что гарантирует отсутствие коллизий и «409 уже закрыто» в рамках одного прогона.
 *
 * Требования к данным:
 *   Перед запуском выполнить: tests/stress/setup/stress-seed.sql
 *   (создаёт грузоместа STRESS-TABLE-CP-0001..2000 в статусе RECEIVED_AT_GATE)
 *
 * Запуск:
 *   k6 run tests/stress/05-receiving-table.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';

const TOTAL_CARGOPLACES = 2000;
const SEED_BARCODE = '4600000099999';

// UUID грузомест STRESS-TABLE-CP-0001..2000 (в том же порядке, что и cargoplace_code).
// Файл генерируется run-all.sh перед запуском теста.
const CP_UUIDS = new SharedArray('table_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-table-cps.json')));

export const options = {
  scenarios: {
    table_flow: {
      // closed-model: ровно TOTAL_CARGOPLACES итераций, каждая — уникальное грузоместо.
      executor: 'shared-iterations',
      vus: 60,
      iterations: TOTAL_CARGOPLACES,
      maxDuration: '8m',
    },
  },
  thresholds: {
    'http_req_duration{step:scan_table_cp}': ['p(95)<250'],
    'http_req_duration{step:scan_box}':      ['p(95)<250'],
    'http_req_duration{step:scan_sku}':      ['p(95)<250'],
    'http_req_duration{step:scan_qr}':       ['p(95)<250'],
    'http_req_duration{step:close_cp}':      ['p(95)<500'],
    // При shared-iterations каждое грузоместо обрабатывается ровно один раз.
    http_req_failed: ['rate<0.02'],
  },
};

export function setup() {
  const token = login('operator', 'operator');
  if (!token) {
    exec.test.abort('setup: не удалось получить токен оператора. Проверьте WMS_URL и учётные данные.');
  }

  const bufferBinId = __ENV.RECEIVING_BIN_ID || '';
  if (!bufferBinId) {
    console.warn(
      '[05-receiving-table] RECEIVING_BIN_ID не задан — шаг scan-buffer будет пропущен.\n' +
      'Для полного теста: source <(bash tests/stress/setup/generate-stress-data.sh)',
    );
  }
  return { token, bufferBinId };
}

export default function (data) {
  const { token, bufferBinId } = data;
  if (!token) return;

  // 404 = грузоместо не найдено; 409 = гонка (два VU взяли одно грузоместо) — редко.
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
  ));

  // exec.scenario.iterationInTest: глобальный уникальный индекс 0 … TOTAL_CARGOPLACES-1.
  // Каждая итерация получает уникальный UUID без коллизий.
  const cpIdx = exec.scenario.iterationInTest;
  const cargoplaceId = CP_UUIDS[cpIdx];
  if (!cargoplaceId) {
    console.warn(`VU${__VU}: no cargoplace UUID at index ${cpIdx} — check stress-seed.sql`);
    return;
  }

  const H = authHeaders(token);
  const qrCode = `STRESS-QR-${pad(__VU, 4)}-${pad(__ITER, 6)}`;
  const boxBarcode = `STRESS-BOX-${pad(__VU, 4)}-${pad(__ITER, 6)}`;

  // 1. scan-cargoplace (table)
  const cpRes = http.post(
    `${BASE_URL}/receiving/table/scan-cargoplace`,
    JSON.stringify({ cargoplace_id: cargoplaceId }),
    { headers: H, tags: { step: 'scan_table_cp' } },
  );

  check(cpRes, {
    'scan_table_cp: 200': (r) => r.status === 200,
  });

  if (cpRes.status !== 200) return;

  sleep(0.05);

  // 2. scan-box
  const boxRes = http.post(
    `${BASE_URL}/receiving/table/scan-box`,
    JSON.stringify({ cargoplace_id: cargoplaceId, box_barcode: boxBarcode }),
    { headers: H, tags: { step: 'scan_box' } },
  );
  check(boxRes, { 'scan_box: 200': (r) => r.status === 200 });
  if (boxRes.status !== 200) return;

  const boxData = JSON.parse(boxRes.body).data;
  const boxId = boxData ? boxData.box_id : null;
  if (!boxId) return;

  sleep(0.05);

  // 3. scan-sku (по barcode → получаем sku_id)
  const skuRes = http.post(
    `${BASE_URL}/receiving/table/scan-sku`,
    JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, barcode: SEED_BARCODE }),
    { headers: H, tags: { step: 'scan_sku' } },
  );
  check(skuRes, { 'scan_sku: 200': (r) => r.status === 200 });
  if (skuRes.status !== 200) return;

  const skuData = JSON.parse(skuRes.body).data;
  const skuId = skuData ? skuData.sku_id : null;
  if (!skuId) return;

  sleep(0.05);

  // 4. scan-qr → создаёт product
  const qrRes = http.post(
    `${BASE_URL}/receiving/table/scan-qr`,
    JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, sku_id: skuId, qr_code: qrCode }),
    { headers: H, tags: { step: 'scan_qr' } },
  );
  check(qrRes, {
    'scan_qr: 200': (r) => r.status === 200,
    'scan_qr: RECEIVED': (r) => {
      try { return JSON.parse(r.body).data.status === 'RECEIVED'; } catch (_) { return false; }
    },
  });
  if (qrRes.status !== 200) return;

  sleep(0.05);

  // 5. close-box
  const closeBoxRes = http.post(
    `${BASE_URL}/receiving/table/close-box`,
    JSON.stringify({ box_id: boxId }),
    { headers: H, tags: { step: 'close_box' } },
  );
  check(closeBoxRes, { 'close_box: 200': (r) => r.status === 200 });

  sleep(0.05);

  // 6. scan-buffer (помещаем грузоместо в буфер BUFFER-01)
  if (bufferBinId) {
    const scanBufRes = http.post(
      `${BASE_URL}/receiving/table/scan-buffer`,
      JSON.stringify({ cargoplace_id: cargoplaceId, buffer_bin_id: bufferBinId }),
      { headers: H, tags: { step: 'scan_buffer' } },
    );
    check(scanBufRes, { 'scan_buffer: 200': (r) => r.status === 200 });
    sleep(0.05);
  }

  // 7. close-cargoplace → создаёт outbox events → Kafka → блокчейн
  const closeCpRes = http.post(
    `${BASE_URL}/receiving/table/close-cargoplace`,
    JSON.stringify({ cargoplace_id: cargoplaceId }),
    { headers: H, tags: { step: 'close_cp' } },
  );
  check(closeCpRes, {
    'close_cp: 200': (r) => r.status === 200,
    'close_cp: TABLE_CLOSED': (r) => {
      try { return JSON.parse(r.body).data.status === 'TABLE_CLOSED'; } catch (_) { return false; }
    },
  });

  sleep(0.1);
}
