// Общая конфигурация для нагрузочных тестов WMS.
// Переменная окружения WMS_URL переопределяет базовый URL.
export const BASE_URL = __ENV.WMS_URL || 'http://localhost:8081';

// Порог: соответствует MVP-критерию «< 250 мс p95 для WMS API»
export const THRESHOLDS_STRICT = {
  http_req_duration: ['p(95)<250', 'p(99)<1000'],
  http_req_failed: ['rate<0.01'],
};

// Порог для auth-эндпоинтов: bcrypt медленнее обычных хендлеров
export const THRESHOLDS_AUTH = {
  http_req_duration: ['p(95)<1500', 'p(99)<3000'],
  http_req_failed: ['rate<0.01'],
};

// Порог для smoke-проверки: любой ответ без паники
export const THRESHOLDS_SMOKE = {
  http_req_duration: ['p(95)<2000'],
  http_req_failed: ['rate<0.05'],
};

// Профиль нагрузки: плавный разгон до целевого TPS
export function rampUpStages(targetVUs) {
  return [
    { duration: '30s', target: Math.ceil(targetVUs * 0.1) },
    { duration: '1m',  target: Math.ceil(targetVUs * 0.5) },
    { duration: '2m',  target: targetVUs },
    { duration: '1m',  target: targetVUs },
    { duration: '30s', target: 0 },
  ];
}

// Профиль нагрузки: постоянная нагрузка (soak)
export function soakStages(targetVUs, durationMin) {
  return [
    { duration: '30s',           target: targetVUs },
    { duration: `${durationMin}m`, target: targetVUs },
    { duration: '15s',           target: 0 },
  ];
}

// Профиль нагрузки: spike
export function spikeStages(peakVUs) {
  return [
    { duration: '10s', target: 5 },
    { duration: '20s', target: peakVUs },
    { duration: '30s', target: peakVUs },
    { duration: '10s', target: 5 },
    { duration: '10s', target: 0 },
  ];
}
