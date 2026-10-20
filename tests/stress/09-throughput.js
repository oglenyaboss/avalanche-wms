/**
 * 09-throughput.js — Сквозной throughput-бенч ВСЕГО проекта (SIGNED API).
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
 * Все 4 финальных вызова стадий ПОДПИСАНЫ (ADR-0001): каждый Receive/PutAway/Pick/Ship
 * идёт draft → программная P-256 WebAuthn-подпись (lib/signer.js, байты 1-в-1 с
 * tests/e2e/softauthn_test.go softSign) → submit. Операторов с активными ключами
 * готовит stress-provision в tests/stress/.signed/operators.json — по записи на VU
 * (сидовые операторы без ключей больше не годятся). Число операторов = VUS.
 *
 * Один сценарий (shared-iterations) → самозавершается на ITERS, без гадания
 * с длительностью отдельного planner-сценария.
 *
 * Метрики: tput_items_stored/picked/shipped — товары, ушедшие в подписанное действие
 * стадии (products_placed/products_shipped ответа submit). Один onchain_events row =
 * одно подписанное ДЕЙСТВИЕ над item_ids[], поэтому «подтверждённый переход» = один
 * товар действия, доехавшего до COMMITTED; по наклону из БД это считает обёртка.
 *
 * Требования:
 *   - tests/stress/setup/stress-throughput-seed.sql применён
 *   - tests/stress/.signed/operators.json — операторы с активными ключами
 *     (готовит `cd tests/e2e && go run ./cmd/stress-provision -operators <VUS>`)
 *   - /tmp/stress-tput-cps.json   — массив UUID грузомест STRESS-TPUT-CP-* (по коду)
 *   - /tmp/stress-tput-dests.json — массив {destinationId, shippingBinId, dispatchCodes[]}
 *   - env: RECEIVING_BIN_ID (BUFFER-01), STORAGE_BIN_ID (A-01-01)
 *   - env (опц.): VUS=30, ITERS=5000, PICK_BATCH=4, ALLOC_EVERY=10, MAX_DURATION=15m,
 *     TAIL_DEADLINE_S=300 (дедлайн доотгрузки хвоста в teardown)
 */
import http from 'k6/http';
import { sleep, group } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter } from 'k6/metrics';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders, pad } from './lib/helpers.js';
import { importSigner, signDraft } from './lib/signer.js';

const CP_UUIDS = new SharedArray('tput_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-tput-cps.json')));
const DESTS = new SharedArray('tput_dests', () =>
  JSON.parse(open('/tmp/stress-tput-dests.json')));

// Операторы с активными ключами (stress-provision). Читается в init-контексте;
// при отсутствии файла — внятный аборт в setup().
let OPERATORS = null;
try {
  OPERATORS = JSON.parse(open('./.signed/operators.json'));
} catch (_) { /* нет файла — аборт в setup() */ }

const itemsStored = new Counter('tput_items_stored');
const itemsPicked = new Counter('tput_items_picked');
const itemsShipped = new Counter('tput_items_shipped');

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
// Операторов ровно по числу VU: VU i берёт operators[(i-1) % len] со своим ключом.
const VUS = Number(__ENV.VUS) || (PACE_RPS > 0 ? 70 : 30);

// Чанки отгрузки: первый чанк 200 товаров; при 409 PICKING_NOT_CONFIRMED (сборка
// части товаров ещё не подтверждена в цепи) пробуем меньший хвост буфера.
const SHIP_CHUNKS = [200, 50, 10];

export const options = {
  scenarios: {
    operators: PACE_RPS > 0 ? {
      executor: 'constant-arrival-rate',
      exec: 'operator',
      rate: PACE_RPS,
      timeUnit: '1s',
      duration: __ENV.PACE_DURATION || '150s',
      preAllocatedVUs: VUS,
      maxVUs: VUS,
    } : {
      executor: 'shared-iterations',
      exec: 'operator',
      vus: VUS,
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

// Последняя ошибка draft внутри signed() (код из error-конверта) — для
// разграничения «сборка не подтверждена» и прочих отказов. Состояние на VU:
// VU выполняет итерацию последовательно, гонок нет.
let lastDraftError = null;

export function setup() {
  const required = { RECEIVING_BIN_ID: __ENV.RECEIVING_BIN_ID, STORAGE_BIN_ID: __ENV.STORAGE_BIN_ID };
  const missing = Object.entries(required).filter(([, v]) => !v).map(([k]) => k);
  if (missing.length > 0) exec.test.abort(`[09-throughput] ABORT: не заданы env: ${missing.join(', ')}`);
  if (!DESTS || DESTS.length === 0) {
    exec.test.abort('[09-throughput] ABORT: /tmp/stress-tput-dests.json пуст — запусти через run-throughput.sh');
  }
  if (!OPERATORS || OPERATORS.length === 0) {
    exec.test.abort('[09-throughput] ABORT: нет tests/stress/.signed/operators.json — прогони `cd tests/e2e && go run ./cmd/stress-provision -operators ' + VUS + '`');
  }
  if (OPERATORS.length < VUS) {
    exec.test.abort(`[09-throughput] ABORT: в operators.json ${OPERATORS.length} операторов, нужно ≥ VUS=${VUS} — прогони ` +
      '`cd tests/e2e && go run ./cmd/stress-provision -operators ' + VUS + '`');
  }

  // Токены: VU i логинится оператором i (у каждого свой ключ и корзина сборки).
  const tokens = [];
  for (let i = 0; i < VUS; i++) {
    const op = OPERATORS[i];
    const t = login(op.username, op.password);
    if (!t) exec.test.abort(`setup: не залогинить ${op.username} (прогони stress-provision, проверь стек)`);
    tokens.push(t);
  }

  return {
    tokens,
    operators: OPERATORS,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
  };
}

// Подписант VU — импортируется один раз и кэшится в переменной уровня модуля
// (инит-контекст у каждого VU свой → кэш живёт на VU).
let vuSigner = null;

export async function operator(data) {
  const { tokens, operators, receivingBinId, storageBinId } = data;
  const token = tokens[(__VU - 1) % tokens.length];
  if (!token) return;
  if (!vuSigner) vuSigner = await importSigner(operators[(__VU - 1) % operators.length]);
  const signer = vuSigner;

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
    await shipOnce(H, signer, sDest, dispIdx);
  }

  const productIds = [];

  // ── Receiving table: одна паллета (грузоместо) = одна коробка с PRODUCTS_PER_CP товарами.
  //    scan-cargoplace / scan-box / scan-sku — по разу на паллету; scan-qr — PRODUCTS_PER_CP раз
  //    (каждый создаёт продукт). close-box ОПУЩЕН: close-cargoplace эмитит receiving-события по
  //    ВСЕМ RECEIVED-товарам независимо от статуса коробки. scan-buffer — по разу на паллету
  //    (амортизирует тяжёлую CloseCargoplaceWithOutbox-транзакцию по N товарам).
  //    close-cargoplace теперь подписан → зовётся сразу после group (async-колбэк group
  //    теряет group-теги, поэтому синхронные сканы остаются в group). ──
  let closeCargoplace = false;
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
    closeCargoplace = true;
  });

  if (closeCargoplace) {
    await signed(H, signer,
      '/receiving/table/close-cargoplace/draft', '/receiving/table/close-cargoplace/submit',
      { cargoplace_id: cargoplaceId });
  }

  if (productIds.length === 0) { await tryPick(H, signer, dest); return; }
  if (THINK) sleep(THINK);

  // ── Putaway → STORED (пакетом: scan-product по каждому, подписанная раскладка
  //    ОДНИМ действием на всю паллету; itemsStored — из products_placed submit-ответа) ──
  let ready = [];
  group('putaway', () => {
    http.post(`${BASE_URL}/putaway/scan-buffer`, JSON.stringify({ buffer_bin_id: receivingBinId }), { headers: H });
    ready = [];
    for (const pid of productIds) {
      const spRes = http.post(`${BASE_URL}/putaway/scan-product`,
        JSON.stringify({ product_id: pid, buffer_bin_id: receivingBinId }), { headers: H });
      if (spRes.status === 200) ready.push(pid);
    }
  });
  if (ready.length > 0) {
    const sbRes = await signed(H, signer,
      '/putaway/scan-storage-bin/draft', '/putaway/scan-storage-bin/submit',
      { product_ids: ready, storage_bin_id: storageBinId });
    if (sbRes && sbRes.status === 200) {
      try { itemsStored.add(JSON.parse(sbRes.body).data.products_placed || 0); } catch (_) {}
    }
  }

  if (THINK) sleep(THINK);

  // ── Pick готовых задач СВОЕГО магазина (allocate создаёт их асинхронно) ──
  await tryPick(H, signer, dest);
}

// signed — draft → программная подпись → submit. Возвращает submit-ответ
// (или null при не-200 draft; код ошибки draft остаётся в lastDraftError).
async function signed(H, signer, draftPath, submitPath, body) {
  lastDraftError = null;
  const draftRes = http.post(`${BASE_URL}${draftPath}`, JSON.stringify(body), { headers: H });
  if (draftRes.status !== 200) {
    try { lastDraftError = (JSON.parse(draftRes.body).error || {}).code || ''; } catch (_) { lastDraftError = ''; }
    return null;
  }
  let draft = null;
  try { draft = JSON.parse(draftRes.body).data || null; } catch (_) {}
  if (!draft || !draft.parts || draft.parts.length === 0) return null;
  const submitBody = await signDraft(signer, draft);
  return http.post(`${BASE_URL}${submitPath}`, JSON.stringify(submitBody), { headers: H });
}

// tryPick — берёт до PICK_BATCH готовых задач магазина dest и сбрасывает корзину
// в его буфер отгрузки ПОДПИСАННЫМ действием (itemsPicked — из products_placed).
// Нет задач — молча выходим (терпим: allocate ещё не догнал).
async function tryPick(H, signer, dest) {
  let picked = 0;
  group('pick', () => {
    const tasksRes = http.get(`${BASE_URL}/assembly/tasks?destination_id=${dest.destinationId}`, { headers: H });
    if (tasksRes.status !== 200) return;
    let tasks = [];
    try { tasks = (JSON.parse(tasksRes.body).data || {}).tasks || []; } catch (_) { return; }
    if (tasks.length === 0) return;

    for (const task of tasks) {
      if (picked >= PICK_BATCH) break;
      if (!task.product_id) continue;
      const pickRes = http.post(`${BASE_URL}/assembly/pick`,
        JSON.stringify({ product_id: task.product_id }), { headers: H });
      if (pickRes.status === 200) picked++;
      if (THINK) sleep(THINK);
    }
  });
  if (picked === 0) return;

  const flushRes = await signed(H, signer,
    '/assembly/scan-shipping-buffer/draft', '/assembly/scan-shipping-buffer/submit',
    { buffer_bin_id: dest.shippingBinId });
  if (flushRes && flushRes.status === 200) {
    try { itemsPicked.add(JSON.parse(flushRes.body).data.products_placed || 0); } catch (_) {}
  }
}

// shipOnce — отгружает один чанк (≤200) из буфера магазина dest рейсом dispatchCodes[dispIdx].
// scan-driver идемпотентен для AT_GATE; уехавший/занятый рейс → non-200 → молча выходим
// (магазин отгрузится следующим тиком новым рейсом). Эмитит 4-й подписанный переход (SHIPPED).
async function shipOnce(H, signer, dest, dispIdx) {
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

  const outcome = await shipChunk(H, signer, dest.shippingBinId, dispatchId, products);
  if (outcome.outcome === 'shipped') itemsShipped.add(outcome.productsShipped);
}

// shipChunk — подписанная отгрузка первых товаров буфера чанками 200 → 50 → 10
// (в порядке скана буфера). 409 PICKING_NOT_CONFIRMED означает, что сборка части
// товаров ещё не подтверждена в цепи → пробуем меньший чанк. Возвращает:
//   { outcome:'shipped', productsShipped, dispatchDeparted } — чанк отгружен;
//   { outcome:'pending' }  — сборка не подтверждена даже на чанке 10;
//   { outcome:'stuck' }    — другой отказ (рейс/буфер/подпись), повтор бессмыслен.
// Счётчик itemsShipped ведёт только каденс (shipOnce), как и до #20; teardown
// считает хвост отдельно.
async function shipChunk(H, signer, binId, dispatchId, products) {
  for (const size of SHIP_CHUNKS) {
    const chunk = products.slice(0, size).map((p) => p.product_id);
    if (chunk.length === 0) return { outcome: 'stuck' };
    const res = await signed(H, signer, '/shipping/ship/draft', '/shipping/ship/submit',
      { buffer_bin_id: binId, dispatch_id: dispatchId, product_ids: chunk });
    if (res && res.status === 200) {
      let shipped = 0;
      let departed = false;
      try {
        const data = JSON.parse(res.body).data || {};
        shipped = data.products_shipped || 0;
        departed = !!data.dispatch_departed;
      } catch (_) {}
      return { outcome: 'shipped', productsShipped: shipped, dispatchDeparted: departed };
    }
    if (lastDraftError !== 'PICKING_NOT_CONFIRMED') return { outcome: 'stuck' };
  }
  return { outcome: 'pending' };
}

// ── teardown: дошипываем ХВОСТ (что каденс не успел до конца прогона). По каждому
//    магазину идём по рейсам, пропуская уехавшие/занятые (scan-driver non-200), и сливаем
//    буфер чанками, пока не опустеет. Основную массу каденс уже отгрузил. Если сборка
//    хвоста ещё не подтверждена в цепи (PICKING_NOT_CONFIRMED на минимальном чанке 10) —
//    спим 2с и повторяем ТОТ ЖЕ рейс, пока TAIL_DEADLINE_S (дефолт 300с) не истечёт. ──
export async function teardown(data) {
  const { tokens, operators } = data;
  if (!tokens || tokens.length === 0) return;
  // teardown идёт на отдельном VU: свой response-callback, иначе ожидаемые 409
  // (PICKING_NOT_CONFIRMED, уехавший рейс) попадут в http_req_failed.
  http.setResponseCallback(http.expectedStatuses({ min: 200, max: 299 }, 404, 409, 422));
  const H = authHeaders(tokens[0]);
  const signer = await importSigner(operators[0]);
  const deadline = Date.now() + ((Number(__ENV.TAIL_DEADLINE_S) || 300) * 1000);

  let shippedTotal = 0;
  for (const dest of DESTS) {
    if (Date.now() > deadline) break;
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
        if (Date.now() > deadline) { destDone = true; break; }
        const bufRes = http.post(`${BASE_URL}/shipping/scan-buffer`,
          JSON.stringify({ buffer_bin_id: dest.shippingBinId }), { headers: H });
        if (bufRes.status !== 200) break;
        let products = [];
        try { products = JSON.parse(bufRes.body).data.products || []; } catch (_) {}
        if (products.length === 0) { destDone = true; break; }   // буфер пуст → магазин готов
        const outcome = await shipChunk(H, signer, dest.shippingBinId, dispatchId, products);
        if (outcome.outcome === 'shipped') {
          shippedTotal += outcome.productsShipped;
          if (outcome.dispatchDeparted) break;        // буфер опустел, рейс уехал → следующий рейс
          continue;                                   // буфер не пуст → грузим следующий чанк
        }
        if (outcome.outcome === 'pending') { sleep(2); continue; } // сборка не подтверждена → ждём и повторяем рейс
        break;                                        // 'stuck' → берём следующий рейс
      }
    }
  }
  console.log(`teardown: дошипнул хвост ${shippedTotal} товаров по ${DESTS.length} магазинам`);
}
