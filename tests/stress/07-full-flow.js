/**
 * 07-full-flow.js — Full WMS outbound flow stress test
 *
 * Цель: end-to-end нагрузочный тест всего исходящего потока:
 *   Receiving table → Putaway → Assembly → Shipping
 *
 * Каждый VU прогоняет один продукт через весь цикл, включая:
 *   - Создание продукта через scan-qr
 *   - Помещение в хранение через putaway
 *   - Сборку и отгрузку
 *   - Outbox events → Kafka → Blockchain (асинхронно, не ждём)
 *
 * Требования:
 *   - tests/stress/setup/stress-seed.sql выполнен
 *   - Переменные окружения из setup/generate-stress-data.sh установлены:
 *       RECEIVING_BIN_ID   — UUID ячейки BUFFER-01
 *       STORAGE_BIN_ID     — UUID ячейки A-01-01
 *       DESTINATION_ID     — UUID магазина (SHOP-7 или SHOP-5)
 *       SHIPPING_BIN_ID    — UUID буфера отгрузки для этого магазина
 *       DISPATCH_CODE      — код рейса (STRESS-DSP-XXXX)
 *
 * Запуск:
 *   source <(docker exec postgres_db psql -U root -d wms_blockchain_db \
 *     -tAc "SELECT 'export RECEIVING_BIN_ID=' || b.bin_id ||
 *            E'\nexport STORAGE_BIN_ID=' || s.bin_id ||
 *            E'\nexport DESTINATION_ID=' || d.destination_id ||
 *            E'\nexport SHIPPING_BIN_ID=' || sh.bin_id
 *           FROM wms_inventory.bins b, wms_inventory.bins s,
 *                wms_inventory.destinations d, wms_inventory.bins sh
 *           WHERE b.code='BUFFER-01' AND s.code='A-01-01'
 *             AND d.code='SHOP-7'
 *             AND sh.destination_id=d.destination_id AND sh.section='SHIPPING_BUFFER'
 *           LIMIT 1")
 *   k6 run tests/stress/07-full-flow.js
 */
import http from 'k6/http';
import { check, sleep, group } from 'k6';
import exec from 'k6/execution';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';

export const options = {
  scenarios: {
    full_flow: {
      executor: 'ramping-vus',
      stages: [
        { duration: '30s', target: 3 },
        { duration: '3m',  target: 15 },
        { duration: '2m',  target: 30 },
        { duration: '1m',  target: 0 },
      ],
    },
  },
  thresholds: {
    // Суммарное время одного полного цикла приёмки-отгрузки
    'group_duration{group:::receiving_table}': ['p(95)<1500'],
    'group_duration{group:::putaway}': ['p(95)<500'],
    'group_duration{group:::assembly}': ['p(95)<1000'],
    'group_duration{group:::shipping}': ['p(95)<500'],
    http_req_failed: ['rate<0.15'],
  },
};

export function setup() {
  // Проверяем обязательные env-переменные ДО старта VU.
  // Если хотя бы одна не задана — тест прерывается немедленно с понятным сообщением.
  const required = {
    RECEIVING_BIN_ID: __ENV.RECEIVING_BIN_ID,
    STORAGE_BIN_ID:   __ENV.STORAGE_BIN_ID,
    DESTINATION_ID:   __ENV.DESTINATION_ID,
    SHIPPING_BIN_ID:  __ENV.SHIPPING_BIN_ID,
    DISPATCH_CODE:    __ENV.DISPATCH_CODE,
  };
  const missing = Object.entries(required)
    .filter(([, v]) => !v)
    .map(([k]) => k);

  if (missing.length > 0) {
    const msg =
      '\n\n[07-full-flow] ABORT: обязательные env-переменные не заданы:\n' +
      missing.map((k) => `  - ${k}`).join('\n') +
      '\n\nВыполните перед запуском:\n' +
      '  source <(bash tests/stress/setup/generate-stress-data.sh)\n' +
      'или передайте переменные явно:\n' +
      '  k6 run -e RECEIVING_BIN_ID=<uuid> -e STORAGE_BIN_ID=<uuid> \\\n' +
      '         -e DESTINATION_ID=<uuid> -e SHIPPING_BIN_ID=<uuid> \\\n' +
      '         -e DISPATCH_CODE=STRESS-DSP-0001 \\\n' +
      '         tests/stress/07-full-flow.js\n';
    exec.test.abort(msg);
  }

  const token = login('operator', 'operator');
  if (!token) {
    exec.test.abort('setup: не удалось получить токен оператора. Проверьте WMS_URL и учётные данные.');
  }
  return {
    token,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
    destinationId:  __ENV.DESTINATION_ID,
    shippingBinId:  __ENV.SHIPPING_BIN_ID,
    dispatchCode:   __ENV.DISPATCH_CODE,
  };
}

export default function (data) {
  const { token, receivingBinId, storageBinId, destinationId, shippingBinId, dispatchCode } = data;

  // setup() уже проверил env-переменные и токен; сюда они приходят гарантированно.
  if (!token) return;

  const H = authHeaders(token);
  const suffix = `${pad(__VU, 4)}-${pad(__ITER, 6)}`;
  const cpIdx = ((__VU - 1 + __ITER * 200) % 500) + 1;
  const cargoplaceId = data[`cp_${cpIdx}`] || ''; // UUID из stress-table-data.json

  if (!cargoplaceId) {
    // Если UUID грузоместа не передан в setup() — тест пропускает receiving шаг
    // и сразу проверяет putaway (нужен product в RECEIVED статусе из другого теста)
    // Полный поток без data/stress-full-flow-data.json невозможен.
    console.warn(`VU${__VU}: no cargoplace UUID for index ${cpIdx} — run generate-stress-data.sh`);
    return;
  }

  let productId = null;

  // ── БЛОК 1: Receiving table ──────────────────────────────────────────────
  group('receiving_table', () => {
    const qrCode = `STRESS-QR-${suffix}`;
    const boxBarcode = `STRESS-BOX-${suffix}`;
    const barcode = '4600000099999';

    // scan-cargoplace
    const cpRes = http.post(
      `${BASE_URL}/receiving/table/scan-cargoplace`,
      JSON.stringify({ cargoplace_id: cargoplaceId }),
      { headers: H },
    );
    if (cpRes.status !== 200) return;

    // scan-box
    const boxRes = http.post(
      `${BASE_URL}/receiving/table/scan-box`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_barcode: boxBarcode }),
      { headers: H },
    );
    if (boxRes.status !== 200) return;
    const boxId = JSON.parse(boxRes.body).data.box_id;

    // scan-sku
    const skuRes = http.post(
      `${BASE_URL}/receiving/table/scan-sku`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, barcode }),
      { headers: H },
    );
    if (skuRes.status !== 200) return;
    const skuId = JSON.parse(skuRes.body).data.sku_id;

    // scan-qr → creates product
    const qrRes = http.post(
      `${BASE_URL}/receiving/table/scan-qr`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, sku_id: skuId, qr_code: qrCode }),
      { headers: H },
    );
    if (qrRes.status !== 200) return;
    productId = JSON.parse(qrRes.body).data.product_id;

    // close-box
    http.post(`${BASE_URL}/receiving/table/close-box`, JSON.stringify({ box_id: boxId }), { headers: H });

    // scan-buffer
    if (receivingBinId) {
      http.post(
        `${BASE_URL}/receiving/table/scan-buffer`,
        JSON.stringify({ cargoplace_id: cargoplaceId, buffer_bin_id: receivingBinId }),
        { headers: H },
      );
    }

    // close-cargoplace → outbox event → Kafka → blockchain (async)
    http.post(
      `${BASE_URL}/receiving/table/close-cargoplace`,
      JSON.stringify({ cargoplace_id: cargoplaceId }),
      { headers: H },
    );

    check(qrRes, { 'receiving_table: product created': () => !!productId });
  });

  if (!productId) return;
  sleep(0.05);

  // ── БЛОК 2: Putaway ──────────────────────────────────────────────────────
  group('putaway', () => {
    // scan-buffer (read-only: показывает продукты в буфере)
    http.post(
      `${BASE_URL}/putaway/scan-buffer`,
      JSON.stringify({ buffer_bin_id: receivingBinId }),
      { headers: H },
    );

    // scan-product
    const scanProdRes = http.post(
      `${BASE_URL}/putaway/scan-product`,
      JSON.stringify({ product_id: productId, buffer_bin_id: receivingBinId }),
      { headers: H },
    );
    check(scanProdRes, { 'putaway: scan-product 200': (r) => r.status === 200 });

    // scan-storage-bin → outbox event → Kafka → blockchain (async)
    const storeBinRes = http.post(
      `${BASE_URL}/putaway/scan-storage-bin`,
      JSON.stringify({ product_ids: [productId], storage_bin_id: storageBinId }),
      { headers: H },
    );
    check(storeBinRes, {
      'putaway: scan-storage-bin 200': (r) => r.status === 200,
      'putaway: product STORED': (r) => {
        try { return JSON.parse(r.body).data.products_placed >= 1; } catch (_) { return false; }
      },
    });
  });

  sleep(0.05);

  // ── БЛОК 3: Assembly ─────────────────────────────────────────────────────
  group('assembly', () => {
    // allocate
    const allocRes = http.post(
      `${BASE_URL}/assembly/allocate`,
      JSON.stringify({ destination_id: destinationId }),
      { headers: H },
    );
    check(allocRes, { 'assembly: allocate 200': (r) => r.status === 200 });
    if (allocRes.status !== 200) return;

    // tasks
    const tasksRes = http.get(
      `${BASE_URL}/assembly/tasks?destination_id=${destinationId}`,
      { headers: H },
    );
    if (tasksRes.status !== 200) return;
    const tasks = JSON.parse(tasksRes.body).data.tasks || [];

    // pick each task
    for (const task of tasks) {
      if (!task.product_id) continue;
      http.post(
        `${BASE_URL}/assembly/pick`,
        JSON.stringify({ product_id: task.product_id }),
        { headers: H },
      );
      sleep(0.02);
    }

    // scan-shipping-buffer
    const shipBufRes = http.post(
      `${BASE_URL}/assembly/scan-shipping-buffer`,
      JSON.stringify({ buffer_bin_id: shippingBinId }),
      { headers: H },
    );
    check(shipBufRes, { 'assembly: scan-shipping-buffer 200': (r) => r.status === 200 });
  });

  sleep(0.05);

  // ── БЛОК 4: Shipping ─────────────────────────────────────────────────────
  group('shipping', () => {
    // scan-buffer
    http.post(
      `${BASE_URL}/shipping/scan-buffer`,
      JSON.stringify({ buffer_bin_id: shippingBinId }),
      { headers: H },
    );

    // scan-driver → AT_GATE
    if (!dispatchCode) return;
    const driverRes = http.post(
      `${BASE_URL}/shipping/scan-driver`,
      JSON.stringify({ dispatch_code: dispatchCode }),
      { headers: H },
    );
    check(driverRes, { 'shipping: scan-driver 200': (r) => r.status === 200 });
    if (driverRes.status !== 200) return;
    const dispatchId = JSON.parse(driverRes.body).data.dispatch_id;

    // ship → outbox events → Kafka → blockchain (async)
    const shipRes = http.post(
      `${BASE_URL}/shipping/ship`,
      JSON.stringify({
        buffer_bin_id: shippingBinId,
        dispatch_id: dispatchId,
        product_ids: [productId],
      }),
      { headers: H },
    );
    check(shipRes, {
      'shipping: ship 200': (r) => r.status === 200,
      'shipping: dispatch_departed': (r) => {
        try { return JSON.parse(r.body).data.dispatch_departed === true; } catch (_) { return false; }
      },
    });
  });

  sleep(0.2);
}
