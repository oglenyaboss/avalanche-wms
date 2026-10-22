/**
 * 08-blockchain-tps.js — Blockchain event generation TPS test
 *
 * Цель: измерить максимальную скорость генерации outbox-событий WMS
 * (синхронная часть blockchain-pipeline) и дать baseline для сравнения
 * с реальным blockchain TPS (асинхронная часть, измеряется в run-all.sh).
 *
 * Архитектура blockchain pipeline:
 *
 *   k6 → WMS API → Postgres + outbox_events  ← этот тест (синхронно)
 *                        ↓ (асинхронно)
 *   Debezium → Kafka → ledger-adapter → contract → onchain_events
 *                        ↑ blockchain TPS измеряется через run-all.sh мониторинг
 *
 * Каждая итерация выполняет putaway одного товара, созданного в setup():
 *   POST /putaway/scan-product          → продукт добавлен в корзину
 *   POST /putaway/scan-storage-bin/draft + /submit → продукт STORED (подписанное действие)
 *
 * Метрика items_placed — товары из ответа submit (products_placed), а не строки
 * outbox: одно подписанное действие покрывает много товаров (warehouse_actions.item_ids).
 *
 * Данные: setup() сам создаёт 5000 товаров (500 грузомест STRESS-TPS-CP-* × 10)
 * через подписанный receiving (lib/bootstrap.js): они приходят в BUFFER-01
 * с on-chain Receive (Status.Accepted), поэтому PutAway — настоящий переход
 * FSM, а не WrongItemStatus для товаров без цепочечной истории.
 * Засев: tests/stress/setup/stress-seed.sql (секция 6, STRESS-TPS-CP-*).
 *
 * Требования:
 *   - RECEIVING_BIN_ID  — UUID ячейки BUFFER-01
 *   - STORAGE_BIN_ID    — UUID ячейки A-01-01
 *   - tests/stress/.signed/operators.json — операторы с ACTIVE-ключами
 *     (`cd tests/e2e && go run ./cmd/stress-provision -operators 100`): один на VU
 *
 * Запуск:
 *   k6 run -e "WMS_URL=http://wms-service:8080" \
 *          -e "RECEIVING_BIN_ID=<uuid>" \
 *          -e "STORAGE_BIN_ID=<uuid>" \
 *          tests/stress/08-blockchain-tps.js
 *
 * После теста run-all.sh ждёт 60 секунд и измеряет (в товарах, не в строках outbox):
 *   - WMS item-generation rate   = delta(товаров подписанных действий) / test_duration
 *   - Blockchain commit rate     = delta(товаров COMMITTED-действий) / elapsed
 *   - Backlog                    = outbox_events без соответствующей записи в onchain_events
 *
 * Целевые метрики (blockchain TPS = 1500/s):
 *   - WMS putaway throughput: >500 events/s (WMS значительно быстрее blockchain-пайплайна)
 *   - Blockchain commit rate: определяется конфигом ledger-adapter:
 *       batch_size=10, batch_timeout=100ms → max ~100 batch-TXs/s → ~1000 events/s теоретически
 *   - Backlog после теста: должен сокращаться, не расти (пайплайн справляется)
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';
import { SharedArray } from 'k6/data';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders } from './lib/helpers.js';
import { importSigner } from './lib/signer.js';
import { loadOperators, signedPost } from './lib/signedflow.js';
import { receiveProducts } from './lib/bootstrap.js';

const TOTAL_PRODUCTS = 5000;
const VUS = Number(__ENV.VUS) || 100;
// Минимум принятых товаров с on-chain Receive: 4750 из 5000 (95%).
const MIN_RECEIVED = 4750;

// Операторы с активными ключами (stress-provision): VU i подписывает оператором i.
const OPERATORS = loadOperators();

// Грузоместа пула STRESS-TPS-CP-001..500 (файл готовит run-all.sh). Товары по ним
// создаёт setup() — id собираются из ответов scan-qr и раздаются VU через setup data.
const CP_UUIDS = new SharedArray('tps_cargoplaces', () =>
  JSON.parse(open('/tmp/stress-tps-cps.json')));

// Счётчик товаров, реально размещённых подписанным действием (products_placed submit-ответа).
// Threshold 'count>=4750' означает: ≥95% из 5000 продуктов успешно помещено на склад.
const itemsPlaced = new Counter('items_placed');

export const options = {
  scenarios: {
    putaway_flood: {
      // closed-model: ровно TOTAL_PRODUCTS итераций, каждая — уникальный продукт.
      // После обработки всех 5000 продуктов тест завершается естественно.
      executor: 'shared-iterations',
      vus: VUS,
      iterations: Number(__ENV.ITER) || TOTAL_PRODUCTS,
      maxDuration: '5m',
    },
  },
  // Подписанный receiving 500 грузомест × 10 товаров — ~17k запросов в setup.
  setupTimeout: '10m',
  thresholds: {
    'http_req_duration{step:scan_product}':     ['p(95)<500', 'p(99)<1000'],
    'http_req_duration{step:scan_storage_bin}': ['p(95)<500', 'p(99)<1000'],
    // При shared-iterations каждый продукт используется ровно один раз.
    http_req_failed: ['rate<0.05'],
    // Ключевой порог: ≥95% из 5000 товаров успешно размещено подписанным действием.
    // Если WMS не справляется — этот порог не пройдёт.
    'items_placed': ['count>=4750'],
  },
};

export async function setup() {
  const required = {
    RECEIVING_BIN_ID: __ENV.RECEIVING_BIN_ID,
    STORAGE_BIN_ID:   __ENV.STORAGE_BIN_ID,
  };
  const missing = Object.entries(required).filter(([, v]) => !v).map(([k]) => k);
  if (missing.length > 0) {
    exec.test.abort(
      '[08-blockchain-tps] ABORT: обязательные env-переменные не заданы: ' +
      missing.join(', '),
    );
  }

  if (OPERATORS.length < VUS) {
    exec.test.abort('[08-blockchain-tps] ABORT: нет операторов с ключами: ' +
      'прогони cd tests/e2e && go run ./cmd/stress-provision -operators 100');
  }

  // Токены: VU i логинится оператором i (у каждого свой ключ подписи).
  const tokens = [];
  for (let i = 0; i < VUS; i++) {
    const op = OPERATORS[i];
    const t = login(op.username, op.password);
    if (!t) {
      exec.test.abort(`setup: не удалось залогинить ${op.username} (прогони stress-provision, проверь стек)`);
    }
    tokens.push(t);
  }

  // Товары с реальной on-chain историей: Receive (close-cargoplace) оператором 0.
  // PutAway в setup не делаем — тест 08 измеряет именно putaway-действия.
  const bootstrapSigner = await importSigner(OPERATORS[0]);
  const productIds = await receiveProducts(authHeaders(tokens[0]), bootstrapSigner, CP_UUIDS, {
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    barcode: 'STRESS-TPS-BC-01',
    qrPrefix: 'STRESS-TPS-QR',
    perCp: 10,
  });
  if (productIds.length < MIN_RECEIVED) {
    exec.test.abort(`[08-blockchain-tps] ABORT: принято ${productIds.length} товаров из ${TOTAL_PRODUCTS} ` +
      `(минимум ${MIN_RECEIVED}): receiving не прошёл — проверь seed и stress-provision`);
  }
  console.log(`setup: принято товаров ${productIds.length} (пул ${CP_UUIDS.length} грузомест × 10)`);

  return {
    tokens,
    operators: OPERATORS,
    productIds,
    receivingBinId: __ENV.RECEIVING_BIN_ID,
    storageBinId:   __ENV.STORAGE_BIN_ID,
  };
}

// Подписант VU — импортируется один раз и кэшируется в переменной уровня модуля
// (инит-контекст у каждого VU свой → кэш живёт на VU).
let vuSigner = null;

export default async function (data) {
  const { tokens, operators, productIds, receivingBinId, storageBinId } = data;
  const opIdx = (__VU - 1) % operators.length;
  const token = tokens[opIdx];
  if (!token) return;
  if (!vuSigner) vuSigner = await importSigner(operators[opIdx]);
  const signer = vuSigner;

  // 404 = продукт не найден (данные не засеяны);
  // 409 = продукт не в статусе RECEIVED (уже обработан конкурентным VU — редко).
  http.setResponseCallback(http.expectedStatuses(
    { min: 200, max: 299 },
    404,
    409,
  ));

  const H = authHeaders(token);

  // exec.scenario.iterationInTest: глобальный уникальный индекс 0…4999.
  const productId = productIds[exec.scenario.iterationInTest];
  if (!productId) {
    console.warn(`VU${__VU}: no product UUID at index ${exec.scenario.iterationInTest}`);
    return;
  }

  // 1. scan-product: добавляем продукт в путэвей-корзину оператора.
  //    Требование WMS: product.status == 'RECEIVED' && product.bin_id == buffer_bin_id.
  const scanProdRes = http.post(
    `${BASE_URL}/putaway/scan-product`,
    JSON.stringify({ product_id: productId, buffer_bin_id: receivingBinId }),
    { headers: H, tags: { step: 'scan_product' } },
  );
  check(scanProdRes, { 'scan_product: 200': (r) => r.status === 200 });
  if (scanProdRes.status !== 200) return;

  // 2. scan-storage-bin (подписанное действие): размещаем продукт → статус STORED.
  //    Побочный эффект: одна строка outbox_events на действие (товары — в item_ids)
  //    → Debezium → Kafka → ledger-adapter → blockchain.
  const storeBin = await signedPost(H, signer,
    '/putaway/scan-storage-bin/draft', '/putaway/scan-storage-bin/submit',
    { product_ids: [productId], storage_bin_id: storageBinId }, { step: 'scan_storage_bin' });
  const storeBinRes = storeBin.submitRes || storeBin.draftRes;
  const placed = check(storeBinRes, {
    'scan_storage_bin: 200': (r) => r.status === 200,
    'scan_storage_bin: products_placed >= 1': (r) => {
      try { return JSON.parse(r.body).data.products_placed >= 1; } catch (_) { return false; }
    },
  });

  // Инкрементируем счётчик товаров только при реальном успехе.
  if (placed) {
    try { itemsPlaced.add(JSON.parse(storeBinRes.body).data.products_placed || 0); } catch (_) {}
  }
}
