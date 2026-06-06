/**
 * 09-throughput.js — Сквозной throughput-бенч ВСЕГО проекта.
 *
 * Отвечает на вопрос «какова реальная пропускная способность системы
 * receiving → putaway → assembly → shipping → on-chain», сняв искусственные
 * тормоза теста 07 (доказанные де-риском — глобальная сериализация allocate):
 *
 *   1) РАЗНОС ПО МАГАЗИНАМ — заказы размазаны по 70 магазинам (stress-throughput-seed, N_DEST),
 *      поэтому per-destination FOR UPDATE-лок allocate не становится глобальным.
 *   2) ПЛАНИРОВЩИК-КАДЕНС — allocate вынесен из горячего пути: он зовётся НЕ каждым
 *      оператором каждую итерацию, а лишь раз в ALLOC_EVERY глобальных итераций,
 *      round-robin по магазинам. iterationInTest уникален → два allocate одновременно
 *      на одном магазине невозможны → драки за лок нет. Тот же POST /assembly/allocate,
 *      просто редкий и разнесённый каденс (как планирование волны в реальном складе).
 *      Операторы лишь берут готовые задачи и терпят «задач ещё нет».
 *
 * Один сценарий (shared-iterations) → самозавершается на ITERS, без гадания
 * с длительностью отдельного planner-сценария.
 *
 * Метрики: tput_items_stored (прошли putaway), tput_items_picked (доехали до
 * READY_TO_SHIP). Реальную items/с харнесс дополнительно считает из БД по наклону.
 *
 * Требования:
 *   - tests/stress/setup/stress-throughput-seed.sql применён
 *   - /tmp/stress-tput-cps.json   — массив UUID грузомест STRESS-TPUT-CP-* (по коду)
 *   - /tmp/stress-tput-dests.json — массив {destinationId, shippingBinId, dispatchCodes[]}
 *   - env: RECEIVING_BIN_ID (BUFFER-01), STORAGE_BIN_ID (A-01-01)
 *   - env (опц.): VUS=30, ITERS=5000, PICK_BATCH=4, ALLOC_EVERY=10, MAX_DURATION=15m
 */
import http from 'k6/http';
import { sleep, group } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter } from 'k6/metrics';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';

const CP_UUIDS = new SharedArray('tput_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-tput-cps.json')));
const DESTS = new SharedArray('tput_dests', () =>
  JSON.parse(open('/tmp/stress-tput-dests.json')));

const itemsStored = new Counter('tput_items_stored');
const itemsPicked = new Counter('tput_items_picked');
const itemsShipped = new Counter('tput_items_shipped');

// Уникальный оператор на VU (изоляция корзины). Сценарий один → __VU ∈ 1..VUS,
// так что OPERATOR_COUNT=VUS достаточно; сид даёт stress-op-01..130.
const OPERATOR_COUNT = Number(__ENV.OPERATOR_COUNT) || 40;
const PICK_BATCH = Number(__ENV.PICK_BATCH) || 4;
const ALLOC_EVERY = Number(__ENV.ALLOC_EVERY) || 10; // allocate раз в N итераций
const SHIP_EVERY = Number(__ENV.SHIP_EVERY) || 4;    // отгрузка раз в N итераций (непрерывная, каденсом)
const SKU_COUNT = Number(__ENV.SKU_COUNT) || 1;      // товары размазаны по N SKU (1 = одно-SKU, BC-01). ДОЛЖЕН совпадать с N_SKU сида.
const PRODUCTS_PER_CP = Number(__ENV.PRODUCTS_PER_CP) || 1; // товаров на грузоместо (реалистичная паллета). ДОЛЖЕН совпадать с N_PER_CP сида.
// Think-time между шагами. Дефолт 0 = бить во весь опор (измеряем ПОТОЛОК системы).
// THINK_MS>0 эмулирует «человеческую» паузу оператора для реалистичного профиля.
const THINK = (Number(__ENV.THINK_MS) || 0) / 1000;

// PACE_RPS>0 → constant-arrival-rate (держим фиксированный темп итераций/с = темп
// генерации), чтобы фронт НЕ перегонял пайплайн и backlog оставался ПЛОСКИМ — честный
// sustained committed при ровном backlog. PACE_RPS=0 (дефолт) → shared-iterations (во весь опор).
const PACE_RPS = Number(__ENV.PACE_RPS) || 0;
export const options = {
  scenarios: {
    operators: PACE_RPS > 0 ? {
      executor: 'constant-arrival-rate',
      exec: 'operator',
      rate: PACE_RPS,
      timeUnit: '1s',
      duration: __ENV.PACE_DURATION || '150s',
      preAllocatedVUs: Number(__ENV.VUS) || 70,
      maxVUs: Number(__ENV.VUS) || 70,
    } : {
      executor: 'shared-iterations',
      exec: 'operator',
      vus: Number(__ENV.VUS) || 30,
      iterations: Number(__ENV.ITERS) || 5000,
      maxDuration: __ENV.MAX_DURATION || '15m',
    },
  },
  teardownTimeout: '10m',
  thresholds: {
    // 404/409/422 — ожидаемые гонки (помечены setResponseCallback), сюда не попадают.
    http_req_failed: ['rate<0.05'],
  },
};

export function setup() {
  const required = { RECEIVING_BIN_ID: __ENV.RECEIVING_BIN_ID, STORAGE_BIN_ID: __ENV.STORAGE_BIN_ID };
  const missing = Object.entries(required).filter(([, v]) => !v).map(([k]) => k);
  if (missing.length > 0) exec.test.abort(`[09-throughput] ABORT: не заданы env: ${missing.join(', ')}`);
  if (!DESTS || DESTS.length === 0) {
    exec.test.abort('[09-throughput] ABORT: /tmp/stress-tput-dests.json пуст — запусти через run-throughput.sh');
  }

  // Токены операторов: каждый VU логинится своим (изоляция корзины сборки).
  const tokens = [];
  for (let i = 1; i <= OPERATOR_COUNT; i++) {
    const t = login(`stress-op-${pad(i, 2)}`, 'stressop');
    if (!t) exec.test.abort(`setup: не залогинить stress-op-${pad(i, 2)} (применён stress-throughput-seed?)`);
    tokens.push(t);
  }

  return {
    tokens,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
  };
}

export function operator(data) {
  const { tokens, receivingBinId, storageBinId } = data;
  const token = tokens[(__VU - 1) % tokens.length];
  if (!token) return;

  // Магазин для pick жёстко закреплён за VU: оператор собирает задачи ОДНОГО
  // магазина и сбрасывает в ЕГО буфер отгрузки (иначе смешаются назначения).
  const dest = DESTS[(__VU - 1) % DESTS.length];

  http.setResponseCallback(http.expectedStatuses({ min: 200, max: 299 }, 404, 409, 422));
  const H = authHeaders(token);
  const iit = exec.scenario.iterationInTest;
  const suffix = `${pad(__VU, 4)}-${pad(__ITER, 6)}`;
  // Штрихкод SKU грузоместа iit — формула ((iit % N_SKU)+1) синхронна с сидом
  // (грузоместо i=iit+1 → SKU ((i-1)%N_SKU)+1 = (iit%N_SKU)+1). Иначе scan-sku 422.
  const skuBarcode = `STRESS-TPUT-BC-${pad((iit % SKU_COUNT) + 1, 2)}`;
  const cargoplaceId = CP_UUIDS[iit] || '';
  if (!cargoplaceId) return;

  // ── Планировщик-каденс: раз в ALLOC_EVERY итераций один (и только один) VU
  //    аллоцирует один магазин round-robin. Разные итерации → разные магазины →
  //    нет конкурентной драки за FOR UPDATE-лок заказов.
  if (iit % ALLOC_EVERY === 0) {
    const planDest = DESTS[Math.floor(iit / ALLOC_EVERY) % DESTS.length];
    http.post(`${BASE_URL}/assembly/allocate`,
      JSON.stringify({ destination_id: planDest.destinationId }), { headers: H });
  }

  // ── Каденс отгрузки: раз в SHIP_EVERY итераций ОДИН VU отгружает ОДИН магазин
  //    round-robin, СВЕЖИМ рейсом (индекс рейса = floor(тик/N_DEST), монотонно растёт →
  //    каждая отгрузка магазина берёт новый SCHEDULED-рейс, уехавшие не переиспользуются;
  //    тики одного магазина на N_DEST врозь → два VU не отгружают его одновременно → нет
  //    гонки за рейс). Так 4-й переход (READY_TO_SHIP→SHIPPED) течёт НЕПРЕРЫВНО по ходу,
  //    а не пачкой в teardown. ──
  if (iit % SHIP_EVERY === 0) {
    const shipTick = Math.floor(iit / SHIP_EVERY);
    const sDest = DESTS[shipTick % DESTS.length];
    const dispIdx = Math.floor(shipTick / DESTS.length);
    shipOnce(H, sDest, dispIdx);
  }

  const productIds = [];

  // ── Receiving table: одна паллета (грузоместо) = одна коробка с PRODUCTS_PER_CP товарами.
  //    scan-cargoplace / scan-box / scan-sku — по разу на паллету; scan-qr — PRODUCTS_PER_CP раз
  //    (каждый создаёт продукт). close-box ОПУЩЕН: close-cargoplace эмитит receiving-события по
  //    ВСЕМ RECEIVED-товарам независимо от статуса коробки. scan-buffer / close-cargoplace — по
  //    разу на паллету (амортизируют тяжёлую CloseCargoplaceWithOutbox-транзакцию по N товарам). ──
  group('receiving', () => {
    const cpRes = http.post(`${BASE_URL}/receiving/table/scan-cargoplace`,
      JSON.stringify({ cargoplace_id: cargoplaceId }), { headers: H });
    if (cpRes.status !== 200) return;

    const boxRes = http.post(`${BASE_URL}/receiving/table/scan-box`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_barcode: `STRESS-TPUT-BOX-${suffix}` }), { headers: H });
    if (boxRes.status !== 200) return;
    const boxId = (JSON.parse(boxRes.body).data || {}).box_id;
    if (!boxId) return;

    const skuRes = http.post(`${BASE_URL}/receiving/table/scan-sku`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, barcode: skuBarcode }), { headers: H });
    if (skuRes.status !== 200) return;
    const skuId = (JSON.parse(skuRes.body).data || {}).sku_id;
    if (!skuId) return;

    for (let j = 0; j < PRODUCTS_PER_CP; j++) {
      const qrRes = http.post(`${BASE_URL}/receiving/table/scan-qr`,
        JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, sku_id: skuId, qr_code: `STRESS-QR-TPUT-${suffix}-${j}` }), { headers: H });
      if (qrRes.status !== 200) continue;
      const pid = (JSON.parse(qrRes.body).data || {}).product_id;
      if (pid) productIds.push(pid);
    }
    if (productIds.length === 0) return;

    // close-box ОБЯЗАТЕЛЕН: scan-buffer переносит в буфер только товары из CLOSED-коробок.
    // Одна коробка на паллету → один close-box на N_PER_CP товаров (амортизируется).
    http.post(`${BASE_URL}/receiving/table/close-box`, JSON.stringify({ box_id: boxId }), { headers: H });

    const bufRes = http.post(`${BASE_URL}/receiving/table/scan-buffer`,
      JSON.stringify({ cargoplace_id: cargoplaceId, buffer_bin_id: receivingBinId }), { headers: H });
    if (bufRes.status !== 200) { productIds.length = 0; }

    http.post(`${BASE_URL}/receiving/table/close-cargoplace`,
      JSON.stringify({ cargoplace_id: cargoplaceId }), { headers: H });
  });

  if (productIds.length === 0) { tryPick(H, dest); return; }
  if (THINK) sleep(THINK);

  // ── Putaway → STORED (пакетом: scan-product по каждому, scan-storage-bin ОДИН на всю паллету) ──
  group('putaway', () => {
    http.post(`${BASE_URL}/putaway/scan-buffer`, JSON.stringify({ buffer_bin_id: receivingBinId }), { headers: H });
    const ready = [];
    for (const pid of productIds) {
      const spRes = http.post(`${BASE_URL}/putaway/scan-product`,
        JSON.stringify({ product_id: pid, buffer_bin_id: receivingBinId }), { headers: H });
      if (spRes.status === 200) ready.push(pid);
    }
    if (ready.length === 0) return;
    const sbRes = http.post(`${BASE_URL}/putaway/scan-storage-bin`,
      JSON.stringify({ product_ids: ready, storage_bin_id: storageBinId }), { headers: H });
    if (sbRes.status === 200) {
      try { itemsStored.add(JSON.parse(sbRes.body).data.products_placed || 0); } catch (_) {}
    }
  });

  if (THINK) sleep(THINK);

  // ── Pick готовых задач СВОЕГО магазина (allocate создаёт их асинхронно) ──
  tryPick(H, dest);
}

// tryPick — берёт до PICK_BATCH готовых задач магазина dest и сбрасывает корзину
// в его буфер отгрузки. Нет задач — молча выходим (терпим: allocate ещё не догнал).
function tryPick(H, dest) {
  group('pick', () => {
    const tasksRes = http.get(`${BASE_URL}/assembly/tasks?destination_id=${dest.destinationId}`, { headers: H });
    if (tasksRes.status !== 200) return;
    let tasks = [];
    try { tasks = (JSON.parse(tasksRes.body).data || {}).tasks || []; } catch (_) { return; }
    if (tasks.length === 0) return;

    let picked = 0;
    for (const task of tasks) {
      if (picked >= PICK_BATCH) break;
      if (!task.product_id) continue;
      const pickRes = http.post(`${BASE_URL}/assembly/pick`,
        JSON.stringify({ product_id: task.product_id }), { headers: H });
      if (pickRes.status === 200) picked++;
      if (THINK) sleep(THINK);
    }
    if (picked === 0) return;

    const flushRes = http.post(`${BASE_URL}/assembly/scan-shipping-buffer`,
      JSON.stringify({ buffer_bin_id: dest.shippingBinId }), { headers: H });
    if (flushRes.status === 200) itemsPicked.add(picked);
  });
}

// shipOnce — отгружает один чанк (≤200) из буфера магазина dest рейсом dispatchCodes[dispIdx].
// scan-driver идемпотентен для AT_GATE; уехавший/занятый рейс → non-200 → молча выходим
// (магазин отгрузится следующим тиком новым рейсом). Эмитит 4-й on-chain переход (SHIPPED).
function shipOnce(H, dest, dispIdx) {
  const codes = dest.dispatchCodes || [];
  if (codes.length === 0) return;
  const dispatchCode = codes[dispIdx % codes.length];
  const drv = http.post(`${BASE_URL}/shipping/scan-driver`,
    JSON.stringify({ dispatch_code: dispatchCode }), { headers: H });
  if (drv.status !== 200) return;
  let dispatchId = null;
  try { dispatchId = JSON.parse(drv.body).data.dispatch_id; } catch (_) { return; }
  if (!dispatchId) return;

  const buf = http.post(`${BASE_URL}/shipping/scan-buffer`,
    JSON.stringify({ buffer_bin_id: dest.shippingBinId }), { headers: H });
  if (buf.status !== 200) return;
  let products = [];
  try { products = JSON.parse(buf.body).data.products || []; } catch (_) { return; }
  if (products.length === 0) return;

  const chunk = products.slice(0, 200).map((p) => p.product_id);
  const shipRes = http.post(`${BASE_URL}/shipping/ship`,
    JSON.stringify({ buffer_bin_id: dest.shippingBinId, dispatch_id: dispatchId, product_ids: chunk }), { headers: H });
  if (shipRes.status === 200) {
    try { itemsShipped.add(JSON.parse(shipRes.body).data.products_shipped || 0); } catch (_) {}
  }
}

// ── teardown: дошипываем ХВОСТ (что каденс не успел до конца прогона). По каждому
//    магазину идём по рейсам, пропуская уехавшие/занятые (scan-driver non-200), и сливаем
//    буфер чанками одним свежим рейсом, пока не опустеет. Основную массу каденс уже отгрузил.
export function teardown(data) {
  const { tokens } = data;
  if (!tokens || tokens.length === 0) return;
  const H = authHeaders(tokens[0]);
  const CHUNK = 200;

  let shippedTotal = 0;
  for (const dest of DESTS) {
    const codes = dest.dispatchCodes || [];
    let destDone = false;
    for (let di = 0; di < codes.length && !destDone; di++) {
      const drvRes = http.post(`${BASE_URL}/shipping/scan-driver`,
        JSON.stringify({ dispatch_code: codes[di] }), { headers: H });
      if (drvRes.status !== 200) continue;          // рейс уехал/занят → следующий
      let dispatchId = null;
      try { dispatchId = JSON.parse(drvRes.body).data.dispatch_id; } catch (_) {}
      if (!dispatchId) continue;

      for (let guard = 0; guard < 2000; guard++) {
        const bufRes = http.post(`${BASE_URL}/shipping/scan-buffer`,
          JSON.stringify({ buffer_bin_id: dest.shippingBinId }), { headers: H });
        if (bufRes.status !== 200) break;
        let products = [];
        try { products = JSON.parse(bufRes.body).data.products || []; } catch (_) {}
        if (products.length === 0) { destDone = true; break; }   // буфер пуст → магазин готов
        const chunk = products.slice(0, CHUNK).map((p) => p.product_id);
        const shipRes = http.post(`${BASE_URL}/shipping/ship`,
          JSON.stringify({ buffer_bin_id: dest.shippingBinId, dispatch_id: dispatchId, product_ids: chunk }), { headers: H });
        if (shipRes.status !== 200) break;          // рейс, вероятно, уехал → берём следующий
        try { shippedTotal += JSON.parse(shipRes.body).data.products_shipped || 0; } catch (_) {}
        let departed = false;
        try { departed = JSON.parse(shipRes.body).data.dispatch_departed; } catch (_) {}
        if (departed) break;                        // буфер опустел, рейс уехал → следующий рейс
      }
    }
  }
  console.log(`teardown: дошипнул хвост ${shippedTotal} товаров по ${DESTS.length} магазинам`);
}
