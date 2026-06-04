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
      // closed-model: каждая итерация — уникальное грузоместо.
      // VUS/ITERS env-настраиваемы (smoke на малом масштабе перед полным прогоном).
      executor: 'shared-iterations',
      vus: Number(__ENV.VUS) || 30,
      iterations: Number(__ENV.ITERS) || 2000,
      maxDuration: __ENV.MAX_DURATION || '15m',
    },
  },
  // Отгрузка вынесена в teardown(): даём время на пакетную развозку буфера.
  teardownTimeout: '5m',
  thresholds: {
    'group_duration{group:::receiving_table}': ['p(95)<1500'],
    'group_duration{group:::putaway}':         ['p(95)<500'],
    // Сборка под конкуренцией: allocate сериализуется на FOR UPDATE всех NEW-заказов,
    // а pick гоняется за общий пул задач (FCFS; 409 у проигравших) — порог мягче.
    'group_duration{group:::assembly}':        ['p(95)<5000'],
    // 409 на pick (проигравший гонку за задачу) и 422 (нет NEW/STORED) ОЖИДАЕМЫ —
    // помечены через setResponseCallback, в http_req_failed не попадают.
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

  // Пул операторов: каждый VU логинится своим оператором, чтобы корзины сборки
  // (assembly_tasks.operator_id) не делились между конкурентными VU. Раздельные
  // операторы — корневой фикс: scan-shipping-buffer (WHERE operator_id=$1) теперь
  // двигает в буфер только товары своего VU.
  const OPERATOR_COUNT = 30;
  const tokens = [];
  for (let i = 1; i <= OPERATOR_COUNT; i++) {
    const username = `stress-op-${pad(i, 2)}`;
    const t = login(username, 'stressop');
    if (!t) {
      exec.test.abort(`setup: не удалось залогинить ${username}. Применён ли stress-seed.sql (раздел 0.1 операторов)?`);
    }
    tokens.push(t);
  }

  // Разбираем коды рейсов (один или несколько через запятую).
  const dispatchCodes = __ENV.DISPATCH_CODE
    .split(',')
    .map((c) => c.trim())
    .filter(Boolean);

  return {
    tokens,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
    destinationId:  __ENV.DESTINATION_ID,
    shippingBinId:  __ENV.SHIPPING_BIN_ID,
    dispatchCodes,
  };
}

export default function (data) {
  const { tokens, receivingBinId, storageBinId, destinationId, shippingBinId } = data;
  // Каждый VU работает под своим оператором из пула (своя корзина сборки).
  const token = tokens[(__VU - 1) % tokens.length];
  if (!token) return;

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
    // Выделенный штрихкод → STRESS-FLOW-SKU (см. stress-seed.sql §0.2): делает тест
    // 07 standalone — allocate матчит только товары потока, не пул теста 06.
    const barcode = 'STRESS-FLOW-BC-01';

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

    // scan-buffer — устанавливает bin_id продукта = receivingBinId.
    // Без этого putaway/scan-product вернёт 409 PRODUCT_NOT_IN_BUFFER и outbox-событие
    // не будет создано. setup() гарантирует наличие receivingBinId.
    const scanBufRes = http.post(
      `${BASE_URL}/receiving/table/scan-buffer`,
      JSON.stringify({ cargoplace_id: cargoplaceId, buffer_bin_id: receivingBinId }),
      { headers: H },
    );
    check(scanBufRes, { 'receiving_table: scan-buffer 200': (r) => r.status === 200 });
    if (scanBufRes.status !== 200) {
      productId = null; // bin_id не установлен → putaway пропустить
    }

    // close-cargoplace → receiving outbox event → Kafka → blockchain (async)
    // Вызываем всегда: receiving-событие записывается даже если scan-buffer упал.
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
    if (scanProdRes.status !== 200) return; // продукт не в буфере → scan-storage-bin не имеет смысла

    // scan-storage-bin → putaway outbox event → Kafka → blockchain (async)
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

  // Отгрузка вынесена в teardown(): детерминированная пакетная развозка буфера
  // READY_TO_SHIP по рейсам. Снимает гонки за рейс под depart-on-empty
  // (scan-driver по departed-рейсу падает) и гарантирует отгрузку всех
  // собранных товаров без потерь.
  sleep(0.1);
}

// teardown — однопоточная (k6 вызывает один раз) пакетная отгрузка накопленного
// буфера READY_TO_SHIP. Гонок за рейс нет: грузим один рейс пакетами по CHUNK
// товаров, при опустошении буфера он переходит в DEPARTED.
export function teardown(data) {
  const { tokens, shippingBinId, dispatchCodes } = data;
  if (!tokens || tokens.length === 0 || !dispatchCodes || dispatchCodes.length === 0) {
    return;
  }
  const H = authHeaders(tokens[0]);
  const CHUNK = 200; // WMS ограничивает product_ids ≤ 200 на один /shipping/ship.
  const dispatchCode = dispatchCodes[0];

  // Подаём рейс к воротам (SCHEDULED → AT_GATE).
  const drvRes = http.post(
    `${BASE_URL}/shipping/scan-driver`,
    JSON.stringify({ dispatch_code: dispatchCode }),
    { headers: H },
  );
  if (drvRes.status !== 200) {
    console.warn(`teardown: scan-driver ${dispatchCode} → ${drvRes.status}, отгрузка пропущена`);
    return;
  }
  let dispatchId = null;
  try { dispatchId = JSON.parse(drvRes.body).data.dispatch_id; } catch (_) {}
  if (!dispatchId) return;

  // Грузим буфер пакетами, пока он не опустеет (последний ship → рейс DEPARTED).
  let shippedTotal = 0;
  for (let guard = 0; guard < 1000; guard++) {
    const bufRes = http.post(
      `${BASE_URL}/shipping/scan-buffer`,
      JSON.stringify({ buffer_bin_id: shippingBinId }),
      { headers: H },
    );
    if (bufRes.status !== 200) break;
    let products = [];
    try { products = JSON.parse(bufRes.body).data.products || []; } catch (_) {}
    if (products.length === 0) break;

    const chunk = products.slice(0, CHUNK).map((p) => p.product_id);
    const shipRes = http.post(
      `${BASE_URL}/shipping/ship`,
      JSON.stringify({ buffer_bin_id: shippingBinId, dispatch_id: dispatchId, product_ids: chunk }),
      { headers: H },
    );
    if (shipRes.status !== 200) {
      console.warn(`teardown: ship → ${shipRes.status}: ${shipRes.body}`);
      break;
    }
    try { shippedTotal += JSON.parse(shipRes.body).data.products_shipped || 0; } catch (_) {}
  }
  console.log(`teardown: отгружено ${shippedTotal} товаров рейсом ${dispatchCode}`);
}
