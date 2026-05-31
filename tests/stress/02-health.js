/**
 * 02-health.js — Health endpoint stress test
 *
 * Цель: измерить максимальную пропускную способность GET /health —
 * «базовую линию» инфраструктуры. /health проверяет Postgres, Kafka
 * и ledger-adapter, поэтому нагрузка на него косвенно отражает
 * состояние всего стека.
 *
 * Целевой TPS согласно docs/architecture/system-overview.md: 1500 TPS.
 * Порог p95 < 250 мс.
 *
 * По умолчанию запускается ramp-сценарий (ramping-vus до 200 VUs).
 * Выбор сценария через переменную окружения STRESS_SCENARIO:
 *
 *   k6 run tests/stress/02-health.js                         # ramp (default)
 *   STRESS_SCENARIO=soak  k6 run tests/stress/02-health.js   # soak 5 min
 *   STRESS_SCENARIO=spike k6 run tests/stress/02-health.js   # spike 500 VUs
 */
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL } from './lib/config.js';

const SCENARIO = __ENV.STRESS_SCENARIO || 'ramp';

function buildOptions() {
  if (SCENARIO === 'soak') {
    return {
      vus: 100,
      duration: '5m',
    };
  }
  if (SCENARIO === 'spike') {
    return {
      stages: [
        { duration: '10s', target: 10 },
        { duration: '20s', target: 500 },
        { duration: '30s', target: 500 },
        { duration: '10s', target: 0 },
      ],
    };
  }
  // default: ramp
  return {
    stages: [
      { duration: '30s', target: 20 },
      { duration: '1m',  target: 100 },
      { duration: '2m',  target: 200 },
      { duration: '1m',  target: 200 },
      { duration: '30s', target: 0 },
    ],
  };
}

export const options = {
  ...buildOptions(),
  thresholds: {
    http_req_duration: SCENARIO === 'spike'
      ? ['p(95)<500', 'p(99)<1000']
      : ['p(95)<250', 'p(99)<500'],
    http_req_failed: ['rate<0.01'],
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/health`);
  check(res, {
    'health: status 200 or 503': (r) => r.status === 200 || r.status === 503,
    'health: has status field': (r) => {
      try { return typeof JSON.parse(r.body).status === 'string'; } catch (_) { return false; }
    },
  });
}
