/**
 * 07-full-flow.js — Full WMS outbound flow stress test
 *
 * Цель: end-to-end нагрузочный тест всего исходящего потока:
 *   Receiving table → Putaway → Assembly → Shipping
 *
 * Каждая итерация прогоняет один продукт через весь цикл, включая:
 *   - Создание продукта через scan-qr
 *   - Помещение в хранение через putaway
 *   - Сборку и отгрузку
 *   - Outbox events → Kafka → Blockchain (асинхронно, не ждём)
 *
 * Модель нагрузки: closed-model (shared-iterations).
 * Каждая итерация получает уникальный UUID грузоместа через exec.scenario.iterationInTest,
 * что гарантирует отсутствие коллизий и «409 уже закрыто» в рамках одного прогона.
 *
 * Требования:
 *   - tests/stress/setup/stress-seed.sql выполнен
 *   - Переменные окружения:
 *       RECEIVING_BIN_ID   — UUID ячейки BUFFER-01
 *       STORAGE_BIN_ID     — UUID ячейки A-01-01
 *       DESTINATION_ID     — UUID магазина SHOP-5
 *       SHIPPING_BIN_ID    — UUID буфера отгрузки для SHOP-5
 *       DISPATCH_CODE      — коды рейсов через запятую (STRESS-FLOW-DSP-0001,...,0010)
 *
 * Запуск:
 *   source <(bash tests/stress/setup/generate-stress-data.sh)
 *   k6 run -e RECEIVING_BIN_ID=$RECEIVING_BIN_ID \
 *          -e STORAGE_BIN_ID=$STORAGE_BIN_ID \
 *          -e DESTINATION_ID=$DESTINATION_ID_07 \
 *          -e SHIPPING_BIN_ID=$SHIPPING_BIN_ID_07 \
 *          -e "DISPATCH_CODE=$DISPATCH_CODE_07" \
 *          tests/stress/07-full-flow.js
 */
import http from 'k6/http';
import { check, sleep, group } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';

// UUID грузомест STRESS-FLOW-CP-0001..2000 (0-indexed, порядок по cargoplace_code).
// Файл генерируется run-all.sh перед запуском теста.
const CP_UUIDS = new SharedArray('flow_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-flow-cps.json')));

export const options = {
  scenarios: {
    full_flow: {
      // closed-model: ровно 2000 итераций, каждая — уникальное грузоместо.
      executor: 'shared-iterations',
      vus: 30,
      iterations: 2000,
      maxDuration: '15m',
    },
  },
  thresholds: {
    'group_duration{group:::receiving_table}': ['p(95)<1500'],
    'group_duration{group:::putaway}':         ['p(95)<500'],
    'group_duration{group:::assembly}':        ['p(95)<1000'],
    'group_duration{group:::shipping}':        ['p(95)<500'],
    // Каждое грузоместо — одна итерация, повторов нет → ошибочных запросов минимум.
    http_req_failed: ['rate<0.05'],
  },
};

export function setup() {
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
      '         -e "DISPATCH_CODE=STRESS-FLOW-DSP-0001,...,STRESS-FLOW-DSP-0010" \\\n' +
      '         tests/stress/07-full-flow.js\n';
    exec.test.abort(msg);
  }

  const token = login('operator', 'operator');
  if (!token) {
    exec.test.abort('setup: не удалось получить токен оператора. Проверьте WMS_URL и учётные данные.');
  }

  // Разбираем коды рейсов (один или несколько через запятую).
  const dispatchCodes = __ENV.DISPATCH_CODE
    .split(',')
    .map((c) => c.trim())
    .filter(Boolean);

  return {
    token,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
    destinationId:  __ENV.DESTINATION_ID,
    shippingBinId:  __ENV.SHIPPING_BIN_ID,
    dispatchCodes,
  };
}

export default function (data) {
  const { token, receivingBinId, storageBinId, destinationId, shippingBinId, dispatchCodes } = data;
  if (!token) return;

  // Ротация кода рейса по глобальному счётчику итерации.
  // С 10 кодами и 2000 итерациями каждый код получает ~200 попыток scan-driver;
  // первая успешная переводит рейс в AT_GATE, остальные получат 409 (ожидаемо).
  const dispatchCode = dispatchCodes[exec.scenario.iterationInTest % dispatchCodes.length];

  // 404 = ресурс не найден; 409 = конфликт (закрытое грузоместо, рейс уже AT_GATE);
  // 422 = нет NEW-заказов или STORED-товаров.
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
    422,
  ));

  const H = authHeaders(token);
  const suffix = `${pad(__VU, 4)}-${pad(__ITER, 6)}`;

  // exec.scenario.iterationInTest: глобальный уникальный индекс 0..1999.
  // Каждая итерация получает своё грузоместо без коллизий.
  const cpIdx = exec.scenario.iterationInTest;
  const cargoplaceId = CP_UUIDS[cpIdx] || '';

  if (!cargoplaceId) {
    console.warn(`VU${__VU}: no cargoplace UUID at index ${cpIdx} — check stress-seed.sql`);
    return;
  }

  let productId = null;
  let pickedProductIds = []; // product IDs actually picked in assembly (may differ from productId)

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
    const boxData = JSON.parse(boxRes.body).data;
    const boxId = boxData ? boxData.box_id : null;
    if (!boxId) return;

    // scan-sku
    const skuRes = http.post(
      `${BASE_URL}/receiving/table/scan-sku`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, barcode }),
      { headers: H },
    );
    if (skuRes.status !== 200) return;
    const skuData = JSON.parse(skuRes.body).data;
    const skuId = skuData ? skuData.sku_id : null;
    if (!skuId) return;

    // scan-qr → creates product
    const qrRes = http.post(
      `${BASE_URL}/receiving/table/scan-qr`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, sku_id: skuId, qr_code: qrCode }),
      { headers: H },
    );
    if (qrRes.status !== 200) return;
    const qrData = JSON.parse(qrRes.body).data;
    productId = qrData ? qrData.product_id : null;

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
    http.post(
      `${BASE_URL}/putaway/scan-buffer`,
      JSON.stringify({ buffer_bin_id: receivingBinId }),
      { headers: H },
    );

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
    const allocRes = http.post(
      `${BASE_URL}/assembly/allocate`,
      JSON.stringify({ destination_id: destinationId }),
      { headers: H },
    );
    check(allocRes, { 'assembly: allocate 200': (r) => r.status === 200 });
    if (allocRes.status !== 200) return;

    // Если нет NEW-заказов или STORED-товаров — выходим без scan-shipping-buffer.
    const allocData = JSON.parse(allocRes.body).data;
    if (!allocData || allocData.allocated_orders === 0) return;

    const tasksRes = http.get(
      `${BASE_URL}/assembly/tasks?destination_id=${destinationId}`,
      { headers: H },
    );
    if (tasksRes.status !== 200) return;
    const tasksBody = JSON.parse(tasksRes.body).data;
    const tasks = (tasksBody && tasksBody.tasks) ? tasksBody.tasks : [];
    if (tasks.length === 0) return;

    // allocate выбирает ЛЮБЫЕ STORED-товары для данного destination,
    // не обязательно тот productId, что создан в блоке receiving_table этой итерации.
    for (const task of tasks) {
      if (!task.product_id) continue;
      const pickRes = http.post(
        `${BASE_URL}/assembly/pick`,
        JSON.stringify({ product_id: task.product_id }),
        { headers: H },
      );
      if (pickRes.status === 200) {
        try {
          const pickData = JSON.parse(pickRes.body).data;
          if (pickData && pickData.product_id) pickedProductIds.push(pickData.product_id);
        } catch (_) {}
      }
      sleep(0.02);
    }

    if (pickedProductIds.length === 0) return;

    const shipBufRes = http.post(
      `${BASE_URL}/assembly/scan-shipping-buffer`,
      JSON.stringify({ buffer_bin_id: shippingBinId }),
      { headers: H },
    );
    check(shipBufRes, { 'assembly: scan-shipping-buffer 200': (r) => r.status === 200 });
  });

  sleep(0.05);

  // ── БЛОК 4: Shipping ─────────────────────────────────────────────────────
  if (pickedProductIds.length === 0) {
    sleep(0.2);
    return;
  }

  group('shipping', () => {
    http.post(
      `${BASE_URL}/shipping/scan-buffer`,
      JSON.stringify({ buffer_bin_id: shippingBinId }),
      { headers: H },
    );

    if (!dispatchCode) return;
    const driverRes = http.post(
      `${BASE_URL}/shipping/scan-driver`,
      JSON.stringify({ dispatch_code: dispatchCode }),
      { headers: H },
    );
    check(driverRes, { 'shipping: scan-driver 200': (r) => r.status === 200 });
    if (driverRes.status !== 200) return;
    const driverData = JSON.parse(driverRes.body).data;
    const dispatchId = driverData ? driverData.dispatch_id : null;
    if (!dispatchId) return;

    // Отгружаем именно подобранные в assembly товары (pickedProductIds).
    const shipRes = http.post(
      `${BASE_URL}/shipping/ship`,
      JSON.stringify({
        buffer_bin_id: shippingBinId,
        dispatch_id: dispatchId,
        product_ids: pickedProductIds,
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
