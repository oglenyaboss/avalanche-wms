/**
 * 06-assembly.js — Assembly flow stress test
 *
 * Цель: нагрузочное тестирование потока сборки заказов:
 *   POST /assembly/allocate
 *   GET  /assembly/tasks?destination_id=<uuid>
 *   POST /assembly/pick  (×N для каждого товара)
 *   POST /assembly/scan-shipping-buffer
 *
 * Требования к данным:
 *   Перед запуском выполнить: tests/stress/setup/stress-seed.sql
 *   (создаёт STORED products и NEW orders для SHOP-5 / SHOP-7)
 *
 * Запуск:
 *   k6 run tests/stress/06-assembly.js
 */
import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { BASE_URL } from './lib/config.js';
import { login, authHeaders } from './lib/helpers.js';

export const options = {
  scenarios: {
    assembly_flow: {
      executor: 'ramping-vus',
      stages: [
        { duration: '30s', target: 5 },
        { duration: '2m',  target: 20 },
        { duration: '3m',  target: 40 },
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    'http_req_duration{step:allocate}': ['p(95)<500', 'p(99)<1000'],
    'http_req_duration{step:tasks}': ['p(95)<250'],
    'http_req_duration{step:pick}': ['p(95)<250', 'p(99)<600'],
    'http_req_duration{step:ship_buffer}': ['p(95)<250'],
    http_req_failed: ['rate<0.20'],
  },
};

export function setup() {
  // Проверяем обязательные env-переменные перед стартом VU.
  const missing = [];
  if (!__ENV.DESTINATION_ID) missing.push('DESTINATION_ID');
  if (!__ENV.SHIPPING_BIN_ID) missing.push('SHIPPING_BIN_ID');

  if (missing.length > 0) {
    const msg =
      '\n\n[06-assembly] ABORT: обязательные env-переменные не заданы:\n' +
      missing.map((k) => `  - ${k}`).join('\n') +
      '\n\nВыполните перед запуском:\n' +
      '  source <(bash tests/stress/setup/generate-stress-data.sh)\n' +
      'или передайте явно:\n' +
      '  k6 run -e DESTINATION_ID=<uuid> -e SHIPPING_BIN_ID=<uuid> \\\n' +
      '         tests/stress/06-assembly.js\n';
    exec.test.abort(msg);
  }

  const token = login('operator', 'operator');
  if (!token) {
    exec.test.abort('setup: не удалось получить токен оператора.');
  }
  return {
    token,
    destinationId: __ENV.DESTINATION_ID,
    shippingBinId: __ENV.SHIPPING_BIN_ID,
  };
}

export default function (data) {
  const { token, destinationId, shippingBinId } = data;
  // setup() гарантирует токен и env-переменные; сюда они приходят корректными.
  if (!token) return;

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
    // 422 INSUFFICIENT_STOCK — нет товаров; 409 ORDER_NOT_NEW — заказы кончились
    if (allocRes.status === 422 || allocRes.status === 409) {
      console.warn(`VU${__VU}: allocate returned ${allocRes.status} — no allocatable orders left`);
    }
    return;
  }

  const allocData = JSON.parse(allocRes.body).data;
  if (!allocData || allocData.allocated_orders === 0) return;

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

  const shipBufRes = http.post(
    `${BASE_URL}/assembly/scan-shipping-buffer`,
    JSON.stringify({ buffer_bin_id: shippingBinId }),
    { headers: H, tags: { step: 'ship_buffer' } },
  );
  check(shipBufRes, {
    'ship_buffer: 200': (r) => r.status === 200,
  });

  sleep(0.1);
}
