import http from 'k6/http';
import exec from 'k6/execution';
import { check, sleep } from 'k6';
import { Counter, Gauge, Rate } from 'k6/metrics';
import { calibration, degradation, profile, stageAt, stressPlan } from './lib/config.js';
import { loadSeed, randomOrder } from './lib/seed.js';
import { buildSummary } from './lib/summary.js';

const profileName = __ENV.PROFILE;

export const options = profile(profileName);

// Целевой RPS, чтобы на дашборде Load рядом с достигнутым (http_reqs) была заданная нагрузка.
const targetRPS = new Gauge('target_rps');
// Старт сценария (мс, unix): по нему summary переводит секунды ступеней в абсолютное время.
const scenarioStart = new Gauge('scenario_start_ts');
// p95 эталонных запросов из setup() (мс), с которым сравнивается критерий «p95 > 3× эталона».
const baselineP95 = new Gauge('baseline_p95_ms');
// Доля запросов медленнее degradation.p95Factor × эталон: p95 > K×эталон ⇔ slow > 5%.
const slow = new Rate('slow');
// Итерации, дошедшие до проверок: расхождение с iterations значит, что часть итераций упала исключением.
const checkedIterations = new Counter('checked_iterations');

const gatewayURL = __ENV.GATEWAY_URL || 'http://gateway:8080';
const orderURL = `${gatewayURL}/api/v1/orders`;
const orderParams = { headers: { 'Content-Type': 'application/json' } };

// calibrate прогревает стек calibration.warmupSec секунд, затем шлёт calibration.requests последовательных
// запросов в темпе первой ступени и возвращает p95 их длительности (мс). Параметры и расчёт setupTimeout
// под них — в lib/config.js.
// Это эталон для критерия «p95 > 3× p95 первой ступени»: темп первой ступени, а не серия без пауз, потому что
// на редких запросах задержка заметно выше (холодные соединения и кэши). Запросы помечены name=calibration
// и не попадают в ступени.
function calibrate(seed) {
  const pauseSec = 1 / stressPlan().stages[0].rate;
  const send = () => {
    const res = http.post(orderURL, JSON.stringify(randomOrder(seed)), {
      ...orderParams,
      tags: { name: 'calibration' },
    });
    if (res.status !== 201) {
      throw new Error(`calibration request failed: status ${res.status}, body ${res.body}`);
    }
    sleep(pauseSec);
    return res.timings.duration;
  };
  const warmupEnd = Date.now() + calibration.warmupSec * 1000;
  while (Date.now() < warmupEnd) {
    send();
  }
  const durations = [];
  for (let i = 0; i < calibration.requests; i++) {
    durations.push(send());
  }
  durations.sort((a, b) => a - b);
  return durations[Math.ceil(durations.length * 0.95) - 1];
}

export function setup() {
  const seed = loadSeed();
  if (profileName === 'stress') {
    seed.baselineP95 = calibrate(seed);
    baselineP95.add(seed.baselineP95);
  }
  return seed;
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
  if (exec.vu.iterationInScenario === 0) {
    scenarioStart.add(exec.scenario.startTime);
  }
  // Тег на VU: его получают и запрос, и checks, и пользовательские метрики этой итерации.
  const at = stageAt(profileName, Date.now() - exec.scenario.startTime);
  exec.vu.tags.stage = String(at.stage);
  exec.vu.tags.phase = at.phase;
  if (at.window !== undefined) {
    exec.vu.tags.window = at.window;
  }
  // Теги VU живут между итерациями: eval перезаписывается на каждой, иначе окно оценки ступени перешло бы
  // на разгон следующей.
  if (at.eval !== undefined) {
    exec.vu.tags.eval = at.eval ? '1' : '0';
  }
  targetRPS.add(at.target);

  const res = http.post(orderURL, JSON.stringify(randomOrder(seed)), {
    ...orderParams,
    tags: { name: 'create_order' },
  });
  check(res, {
    'status is 201': (r) => r.status === 201,
    'has id': (r) => orderID(r) !== undefined,
  });
  checkedIterations.add(1);
  if (seed.baselineP95 !== undefined) {
    slow.add(res.timings.duration > degradation.p95Factor * seed.baselineP95);
  }
}

export function handleSummary(data) {
  return buildSummary(data, profileName, __ENV.RUN_ID || 'local');
}
