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
 * Финальные вызовы стадий (close-cargoplace, scan-storage-bin, scan-shipping-buffer,
 * ship) — подписанные пары draft → P-256 WebAuthn-подпись → submit (lib/signedflow.js).
 * Шип в teardown ретраит чанками 200 → 50 → 10 на PICKING_NOT_CONFIRMED.
 *
 * Модель нагрузки: closed-model (shared-iterations).
 * Каждая итерация получает уникальный UUID грузоместа через exec.scenario.iterationInTest,
 * что гарантирует отсутствие коллизий и «409 уже закрыто» в рамках одного прогона.
 *
 * Требования:
 *   - tests/stress/setup/stress-seed.sql выполнен
 *   - tests/stress/.signed/operators.json — операторы с ACTIVE-ключами
 *     (`cd tests/e2e && go run ./cmd/stress-provision -operators 100`):
 *     один оператор на VU (своя корзина сборки и свой ключ подписи)
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
import { importSigner } from './lib/signer.js';
import { loadOperators, signedPost } from './lib/signedflow.js';

// Операторы с активными ключами (stress-provision): VU i подписывает оператором i.
const OPERATORS = loadOperators();
const VUS = Number(__ENV.VUS) || 30;

// Чанки подписанной отгрузки (teardown): первый — 200, это лимит WMS на один ship.
const SHIP_CHUNKS = [200, 50, 10];

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
      vus: VUS,
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

  // Пул операторов: каждый VU — свой оператор с ACTIVE-ключом (stress-provision).
  // Корзины сборки (assembly_tasks.operator_id) не делятся между VU, и у каждого
  // VU свой ключ подписи. Сидовые операторы без ключей больше не годятся.
  if (OPERATORS.length < VUS) {
    exec.test.abort('[07-full-flow] ABORT: нет операторов с ключами: ' +
      'прогони cd tests/e2e && go run ./cmd/stress-provision -operators 100');
  }
  const tokens = [];
  for (let i = 0; i < VUS; i++) {
    const op = OPERATORS[i];
    const t = login(op.username, op.password);
    if (!t) {
      exec.test.abort(`setup: не удалось залогинить ${op.username} (прогони stress-provision, проверь стек)`);
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
    operators: OPERATORS,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
    destinationId:  __ENV.DESTINATION_ID,
    shippingBinId:  __ENV.SHIPPING_BIN_ID,
    dispatchCodes,
  };
}

// Подписант VU — импортируется один раз и кэшируется в переменной уровня модуля
// (инит-контекст у каждого VU свой → кэш живёт на VU).
let vuSigner = null;

export default async function (data) {
  const { tokens, operators, receivingBinId, storageBinId, destinationId, shippingBinId } = data;
  // Каждый VU работает своим оператором из пула (своя корзина сборки + свой ключ).
  const opIdx = (__VU - 1) % operators.length;
  const token = tokens[opIdx];
  if (!token) return;
  if (!vuSigner) vuSigner = await importSigner(operators[opIdx]);
  const signer = vuSigner;

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
  // Флаги выноса подписанных вызовов из group(): async-колбэк group теряет
  // group-теги, поэтому подписанная пара зовётся сразу после своего group.
  let closeCargoplace = false;
  let readyToPlace = false;
  let cartToFlush = false;

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

    // close-cargoplace — подписанная пара, зовётся после group (см. флаги выше).
    // Вызываем всегда: receiving-событие записывается даже если scan-buffer упал.
    closeCargoplace = true;

    check(qrRes, { 'receiving_table: product created': () => !!productId });
  });

  if (closeCargoplace) {
    await signedPost(H, signer,
      '/receiving/table/close-cargoplace/draft', '/receiving/table/close-cargoplace/submit',
      { cargoplace_id: cargoplaceId });
  }

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

    // scan-storage-bin — подписанная пара, зовётся после group (см. флаги выше).
    readyToPlace = true;
  });

  if (readyToPlace) {
    const store = await signedPost(H, signer,
      '/putaway/scan-storage-bin/draft', '/putaway/scan-storage-bin/submit',
      { product_ids: [productId], storage_bin_id: storageBinId });
    const storeBinRes = store.submitRes || store.draftRes;
    check(storeBinRes, {
      'putaway: scan-storage-bin 200': (r) => r.status === 200,
      'putaway: product STORED': (r) => {
        try { return JSON.parse(r.body).data.products_placed >= 1; } catch (_) { return false; }
      },
    });
  }

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

    // scan-shipping-buffer — подписанная пара, зовётся после group (см. флаги выше).
    cartToFlush = true;
  });

  if (cartToFlush) {
    const flush = await signedPost(H, signer,
      '/assembly/scan-shipping-buffer/draft', '/assembly/scan-shipping-buffer/submit',
      { buffer_bin_id: shippingBinId });
    const shipBufRes = flush.submitRes || flush.draftRes;
    check(shipBufRes, { 'assembly: scan-shipping-buffer 200': (r) => r.status === 200 });
  }

  sleep(0.05);

  // Отгрузка вынесена в teardown(): детерминированная пакетная развозка буфера
  // READY_TO_SHIP по рейсам. Снимает гонки за рейс под depart-on-empty
  // (scan-driver по departed-рейсу падает) и гарантирует отгрузку всех
  // собранных товаров без потерь.
  sleep(0.1);
}

// teardown — однопоточная (k6 вызывает один раз) пакетная отгрузка накопленного
// буфера READY_TO_SHIP. Гонок за рейс нет: грузим один рейс чанками, при
// опустошении буфера он переходит в DEPARTED.
export async function teardown(data) {
  const { tokens, operators, shippingBinId, dispatchCodes } = data;
  if (!tokens || tokens.length === 0 || !dispatchCodes || dispatchCodes.length === 0) {
    return;
  }
  // teardown идёт на отдельном VU: свой response-callback, иначе ожидаемые 409
  // (PICKING_NOT_CONFIRMED, уехавший рейс) попадут в http_req_failed.
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
    422,
  ));
  const H = authHeaders(tokens[0]);
  const signer = await importSigner(operators[0]);
  const dispatchCode = dispatchCodes[0];
  // Дедлайн хвоста: сборка ещё не подтверждена в цепи → ждём и повторяем.
  const deadline = Date.now() + ((Number(__ENV.TAIL_DEADLINE_S) || 300) * 1000);

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

  // Грузим буфер чанками, пока он не опустеет (последний ship → рейс DEPARTED).
  let shippedTotal = 0;
  for (let guard = 0; guard < 1000; guard++) {
    if (Date.now() > deadline) {
      console.warn('teardown: дедлайн хвоста истёк — часть товаров осталась в буфере');
      break;
    }
    const bufRes = http.post(
      `${BASE_URL}/shipping/scan-buffer`,
      JSON.stringify({ buffer_bin_id: shippingBinId }),
      { headers: H },
    );
    if (bufRes.status !== 200) break;
    let products = [];
    try { products = JSON.parse(bufRes.body).data.products || []; } catch (_) {}
    if (products.length === 0) break;

    const outcome = await shipChunk(H, signer, shippingBinId, dispatchId, products);
    if (outcome.outcome === 'shipped') {
      shippedTotal += outcome.shipped;
      continue;
    }
    if (outcome.outcome === 'pending') {
      // Сборка хвоста ещё не подтверждена в цепи (PICKING_NOT_CONFIRMED даже на
      // чанке 10) → ждём и повторяем тот же рейс.
      sleep(2);
      continue;
    }
    console.warn('teardown: ship отказал — отгрузка остановлена');
    break;
  }
  console.log(`teardown: отгружено ${shippedTotal} товаров рейсом ${dispatchCode}`);
}

// shipChunk — подписанная отгрузка первых товаров буфера чанками 200 → 50 → 10
// (в порядке скана буфера). 409 PICKING_NOT_CONFIRMED означает, что сборка части
// товаров ещё не подтверждена в цепи → пробуем меньший чанк:
//   {outcome:'shipped', shipped} — чанк отгружен;
//   {outcome:'pending'}  — не подтверждено даже на чанке 10 (повторим позже);
//   {outcome:'stuck'}    — другой отказ (рейс/буфер/подпись), повтор бессмыслен.
async function shipChunk(H, signer, binId, dispatchId, products) {
  for (const size of SHIP_CHUNKS) {
    const chunk = products.slice(0, size).map((p) => p.product_id);
    if (chunk.length === 0) return { outcome: 'stuck' };
    const res = await signedPost(H, signer,
      '/shipping/ship/draft', '/shipping/ship/submit',
      { buffer_bin_id: binId, dispatch_id: dispatchId, product_ids: chunk });
    if (res.submitRes && res.submitRes.status === 200) {
      let shipped = 0;
      try { shipped = JSON.parse(res.submitRes.body).data.products_shipped || 0; } catch (_) {}
      return { outcome: 'shipped', shipped };
    }
    if (res.draftError !== 'PICKING_NOT_CONFIRMED') return { outcome: 'stuck' };
  }
  return { outcome: 'pending' };
}
