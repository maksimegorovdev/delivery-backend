import http from 'k6/http';
import { check } from 'k6';
import { Gauge } from 'k6/metrics';
import { profile, targetRate } from './lib/config.js';
import { loadSeed, randomOrder } from './lib/seed.js';

export const options = profile(__ENV.PROFILE);

// Целевой RPS, чтобы на дашборде Load рядом с достигнутым (http_reqs) была заданная нагрузка.
const targetRPS = new Gauge('target_rps');
// Считается на init, до того как k6 нормализует options: в VU сценарий уже не в исходной форме.
const currentTargetRate = targetRate(__ENV.PROFILE);

const gatewayURL = __ENV.GATEWAY_URL || 'http://gateway:8080';

export function setup() {
  return loadSeed();
}

// orderID возвращает data.id из ответа или undefined, если тело не JSON (например, 404 от прокси).
function orderID(res) {
  try {
    return res.json('data.id');
  } catch (e) {
    return undefined;
  }
}

export default function (seed) {
  targetRPS.add(currentTargetRate);
  const res = http.post(`${gatewayURL}/api/v1/orders`, JSON.stringify(randomOrder(seed)), {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'create_order' },
  });
  check(res, {
    'status is 201': (r) => r.status === 201,
    'has id': (r) => orderID(r) !== undefined,
  });
}
