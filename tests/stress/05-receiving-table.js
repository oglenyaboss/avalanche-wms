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
 * Требования к данным:
 *   Перед запуском выполнить: tests/stress/setup/stress-seed.sql
 *   (создаёт грузоместа STRESS-TABLE-CP-XXXX в статусе RECEIVED_AT_GATE)
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

const TOTAL_CARGOPLACES = 500;

// Barcode и SKU ID берутся из seed.sql: 'E2E Seed Outbound SKU' / '4600000099999'
// SKU ID будет запрошен через scan-sku по barcode — не нужно хардкодить UUID.
const SEED_BARCODE = '4600000099999';

// UUID грузомест STRESS-TABLE-CP-0001..0500 (в том же порядке, что и cargoplace_code).
// Файл генерируется run-all.sh перед запуском теста.
// Для локального запуска: psql -tAc "SELECT COALESCE(json_agg(cargoplace_id::text
//   ORDER BY cargoplace_code), '[]') FROM wms_inventory.cargoplaces
//   WHERE cargoplace_code LIKE 'STRESS-TABLE-CP-%'" | tr -d '[:space:]'
//   > /tmp/stress-table-cps.json
const CP_UUIDS = new SharedArray('table_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-table-cps.json')));

export const options = {
  scenarios: {
    table_flow: {
      executor: 'ramping-vus',
      stages: [
        { duration: '30s', target: 5 },
        { duration: '2m',  target: 30 },
        { duration: '3m',  target: 60 },
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    'http_req_duration{step:scan_table_cp}': ['p(95)<250'],
    'http_req_duration{step:scan_box}': ['p(95)<250'],
    'http_req_duration{step:scan_sku}': ['p(95)<250'],
    'http_req_duration{step:scan_qr}': ['p(95)<250'],
    'http_req_duration{step:close_cp}': ['p(95)<500'],
    http_req_failed: ['rate<0.15'],
  },
};

export function setup() {
  const token = login('operator', 'operator');
  if (!token) {
    exec.test.abort('setup: не удалось получить токен оператора. Проверьте WMS_URL и учётные данные.');
  }

  // Резолвим UUID буфера приёмки через env-переменную.
  // Если не задана — тест всё равно запустится, но пропустит шаг scan-buffer
  // (это допустимо: close-cargoplace создаёт outbox event независимо от буфера).
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

  // 404 = грузоместо не найдено; 409 = грузоместо уже закрыто (нормально после обработки всех 500).
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
  ));

  // UUID грузоместа для этого VU/ITER (0-indexed в CP_UUIDS).
  // Множитель = пиковый maxVUs (60): каждый round сдвигается на 60 позиций,
  // все 500 грузомест покрываются за ceil(500/60)=9 итераций без пробелов.
  const cpIdx = ((__VU - 1 + (__ITER * 60)) % TOTAL_CARGOPLACES);
  const cargoplaceId = CP_UUIDS[cpIdx];
  if (!cargoplaceId) return; // данные не засеяны

  const H = authHeaders(token);

  // Уникальный QR-код для этой итерации (VU + ITER гарантируют уникальность)
  const qrCode = `STRESS-QR-${pad(__VU, 4)}-${pad(__ITER, 6)}`;
  const boxBarcode = `STRESS-BOX-${pad(__VU, 4)}-${pad(__ITER, 6)}`;

  // 1. scan-cargoplace (table)
  const cpRes = http.post(
    `${BASE_URL}/receiving/table/scan-cargoplace`,
    JSON.stringify({ cargoplace_id: cargoplaceId }),
    { headers: H, tags: { step: 'scan_table_cp' } },
  );

  const cpOk = check(cpRes, {
    'scan_table_cp: 200 or 404/409': (r) => [200, 404, 409].includes(r.status),
  });

  if (cpRes.status !== 200) {
    // Грузоместо не найдено или уже в работе — пропускаем итерацию
    return;
  }

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
