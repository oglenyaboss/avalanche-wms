/**
 * 06-assembly.js — Assembly flow stress test
 *
 * Цель: нагрузочное тестирование потока сборки заказов:
 *   POST /assembly/allocate
 *   GET  /assembly/tasks?destination_id=<uuid>
 *   POST /assembly/pick  (×N для каждого товара)
 *   POST /assembly/scan-shipping-buffer/draft + /submit  (подписанное действие)
 *
 * setup() сам готовит пул товаров через подписанный receiving/putaway
 * (lib/bootstrap.js): прямой INSERT в products оставлял товары на контракте в
 * Status.None, и Pick отклонялся (WrongItemStatus).
 *
 * Требования к данным:
 *   Перед запуском выполнить: tests/stress/setup/stress-seed.sql
 *   (создаёт 100 грузомест STRESS-ASM-CP-* и 1000 NEW-заказов STRESS-ORD-*
 *   для магазина STRESS-SHOP-06)
 *   + tests/stress/.signed/operators.json — операторы с ACTIVE-ключами
 *     (`cd tests/e2e && go run ./cmd/stress-provision -operators 100`):
 *     один оператор на VU (своя корзина сборки и свой ключ подписи).
 *
 * Запуск:
 *   k6 run -e RECEIVING_BIN_ID=<uuid> -e STORAGE_BIN_ID=<uuid> \
 *          -e DESTINATION_ID=<uuid> -e SHIPPING_BIN_ID=<uuid> \
 *          tests/stress/06-assembly.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders } from './lib/helpers.js';
import { importSigner } from './lib/signer.js';
import { loadOperators, signedPost } from './lib/signedflow.js';
import { receiveProducts, putawayProducts } from './lib/bootstrap.js';

// Максимум VU в рампе — столько же операторов и токенов нужно в setup().
const MAX_VUS = 40;

// Операторы с активными ключами (stress-provision): VU i подписывает оператором i.
const OPERATORS = loadOperators();

// Грузоместа пула STRESS-ASM-CP-001..100 (файл готовит run-all.sh).
const CP_UUIDS = new SharedArray('assembly_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-asm-cps.json')));

// Минимум товаров, размещённых на склад подписанным putaway: 950 из 1000 (95%).
const MIN_STORED = 950;

export const options = {
  scenarios: {
    assembly_flow: {
      executor: 'ramping-vus',
      stages: [
        { duration: '30s', target: 5 },
        { duration: '2m',  target: 20 },
        { duration: '3m',  target: MAX_VUS },
        { duration: '30s', target: 0 },
      ],
    },
  },
  // Подписанные draft/submit медленные (P-256) — setup готовит 1000 товаров.
  setupTimeout: '10m',
  thresholds: {
    'http_req_duration{step:allocate}': ['p(95)<500', 'p(99)<1000'],
    'http_req_duration{step:tasks}': ['p(95)<250'],
    'http_req_duration{step:pick}': ['p(95)<250', 'p(99)<600'],
    'http_req_duration{step:ship_buffer}': ['p(95)<250'],
    http_req_failed: ['rate<0.20'],
  },
};

export async function setup() {
  // Проверяем обязательные env-переменные перед стартом VU.
  const missing = [];
  if (!__ENV.DESTINATION_ID) missing.push('DESTINATION_ID');
  if (!__ENV.SHIPPING_BIN_ID) missing.push('SHIPPING_BIN_ID');
  if (!__ENV.RECEIVING_BIN_ID) missing.push('RECEIVING_BIN_ID');
  if (!__ENV.STORAGE_BIN_ID) missing.push('STORAGE_BIN_ID');

  if (missing.length > 0) {
    const msg =
      '\n\n[06-assembly] ABORT: обязательные env-переменные не заданы:\n' +
      missing.map((k) => `  - ${k}`).join('\n') +
      '\n\nВыполните перед запуском:\n' +
      '  source <(bash tests/stress/setup/generate-stress-data.sh)\n' +
      'или передайте явно:\n' +
      '  k6 run -e RECEIVING_BIN_ID=<uuid> -e STORAGE_BIN_ID=<uuid> \\\n' +
      '         -e DESTINATION_ID=<uuid> -e SHIPPING_BIN_ID=<uuid> \\\n' +
      '         tests/stress/06-assembly.js\n';
    exec.test.abort(msg);
  }

  if (OPERATORS.length < MAX_VUS) {
    exec.test.abort('[06-assembly] ABORT: нет операторов с ключами: ' +
      'прогони cd tests/e2e && go run ./cmd/stress-provision -operators 100');
  }

  // Токены: VU i работает своим оператором — своя корзина сборки и свой ключ подписи.
  const tokens = [];
  for (let i = 0; i < MAX_VUS; i++) {
    const op = OPERATORS[i];
    const t = login(op.username, op.password);
    if (!t) {
      exec.test.abort(`setup: не удалось залогинить ${op.username} (прогони stress-provision, проверь стек)`);
    }
    tokens.push(t);
  }

  // Пул товаров с реальной on-chain историей: Receive (close-cargoplace) →
  // PutAway (scan-storage-bin), оператором 0. Без этого Pick отклоняется
  // контрактом (Status.None ≠ PutAway).
  const bootstrapSigner = await importSigner(OPERATORS[0]);
  const bootstrapHeaders = authHeaders(tokens[0]);
  const productIds = await receiveProducts(bootstrapHeaders, bootstrapSigner, CP_UUIDS, {
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    barcode: 'STRESS-ASM-BC-01',
    qrPrefix: 'STRESS-ASM-QR',
    perCp: 10,
  });
  const stored = await putawayProducts(bootstrapHeaders, bootstrapSigner, productIds, {
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId: __ENV.STORAGE_BIN_ID,
  });
  if (stored < MIN_STORED) {
    exec.test.abort(`[06-assembly] ABORT: размещено ${stored} товаров из ${CP_UUIDS.length * 10} ` +
      `(минимум ${MIN_STORED}): receiving/putaway не прошли — проверь seed и stress-provision`);
  }
  console.log(`setup: товаров на складе ${stored} (пул ${CP_UUIDS.length} грузомест × 10)`);

  return {
    tokens,
    operators: OPERATORS,
    destinationId: __ENV.DESTINATION_ID,
    shippingBinId: __ENV.SHIPPING_BIN_ID,
  };
}

// Подписант VU — импортируется один раз и кэшируется в переменной уровня модуля
// (инит-контекст у каждого VU свой → кэш живёт на VU).
let vuSigner = null;

export default async function (data) {
  const { tokens, operators, destinationId, shippingBinId } = data;
  // setup() гарантирует токены и env-переменные; сюда они приходят корректными.
  const opIdx = (__VU - 1) % operators.length;
  const token = tokens[opIdx];
  if (!token) return;
  if (!vuSigner) vuSigner = await importSigner(operators[opIdx]);
  const signer = vuSigner;

  // 409 = нет NEW-заказов; 422 = нет STORED-товаров (нормально после исчерпания пула).
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
    422,
  ));

  const H = authHeaders(token);

  // 1. Allocate — назначает товары на первый доступный NEW заказ для магазина
  const allocRes = http.post(
    `${BASE_URL}/assembly/allocate`,
    JSON.stringify({ destination_id: destinationId }),
    { headers: H, tags: { step: 'allocate' } },
  );

  const allocOk = check(allocRes, {
    'allocate: 200': (r) => r.status === 200,
  });

  if (!allocOk) {
    // 422 INSUFFICIENT_STOCK — нет товаров; 409 ORDER_NOT_NEW — заказы кончились.
    // Пул исчерпан — не долбим allocate в горячем цикле, ждём следующего VU.
    if (allocRes.status === 422 || allocRes.status === 409) {
      console.warn(`VU${__VU}: allocate returned ${allocRes.status} — no allocatable orders left`);
      sleep(1);
    }
    return;
  }

  const allocData = JSON.parse(allocRes.body).data;
  if (!allocData || allocData.allocated_orders === 0) {
    // Пул заказов/стока исчерпан: 200 без аллокаций — дальше без паузы нет смысла.
    sleep(1);
    return;
  }

  sleep(0.1);

  // 2. GetTasks — получаем список задач по сборке
  const tasksRes = http.get(
    `${BASE_URL}/assembly/tasks?destination_id=${destinationId}`,
    { headers: H, tags: { step: 'tasks' } },
  );
  check(tasksRes, { 'tasks: 200': (r) => r.status === 200 });
  if (tasksRes.status !== 200) return;

  const tasksData = JSON.parse(tasksRes.body).data;
  const tasks = (tasksData && tasksData.tasks) ? tasksData.tasks : [];
  if (tasks.length === 0) return;

  sleep(0.05);

  // 3. Pick — подбираем каждый товар
  for (const task of tasks) {
    const productId = task.product_id;
    if (!productId) continue;

    const pickRes = http.post(
      `${BASE_URL}/assembly/pick`,
      JSON.stringify({ product_id: productId }),
      { headers: H, tags: { step: 'pick' } },
    );
    check(pickRes, {
      'pick: 200': (r) => r.status === 200,
    });
    sleep(0.03);
  }

  // 4. scan-shipping-buffer — перекладываем корзину в буфер отгрузки
  if (!shippingBinId) {
    console.warn(`VU${__VU}: SHIPPING_BIN_ID not set, skipping scan-shipping-buffer`);
    return;
  }

  const shipBuf = await signedPost(H, signer,
    '/assembly/scan-shipping-buffer/draft', '/assembly/scan-shipping-buffer/submit',
    { buffer_bin_id: shippingBinId }, { step: 'ship_buffer' });
  const shipBufRes = shipBuf.submitRes || shipBuf.draftRes;
  check(shipBufRes, {
    'ship_buffer: 200': (r) => r.status === 200,
  });

  sleep(0.1);
}
