// Профили нагрузки. Выбираются env PROFILE; открытая модель (arrival rate): RPS задаётся,
// а не получается, поэтому медленная система не снижает нагрузку сама себе.

// Критерий деградации ступени (см. спеку load, «Критерий предела»). Числа приняты; менять — через спеку.
export const degradation = {
  maxErrorRate: 0.01, // доля http_req_failed на плато
  p95Factor: 3, // p95 плато > 3× p95 первой ступени
  minRPSShare: 0.95, // достигнутый RPS < 95% целевого
};

// Разметка ступеней: ступень = разгон + плато. Метрики плато считаются отдельно (тег phase).
const stress = {
  // Мелкая лестница с остановкой по критерию предела: абсолютный предел зависит от машины и лимитов
  // контейнеров, поэтому числа не подгоняются под один результат, а прогон сам останавливается на пределе.
  start: 25,
  step: 25,
  max: 1000,
  rampSec: 30,
  plateauSec: 90,
  preAllocatedVUs: 100,
  maxVUs: 500,
};

// Через сколько секунд плато начинать оценивать пороги abort: первые секунды выборка слишком мала, и короткий
// всплеск задержки даёт ложную деградацию. Оценка идёт по всем запросам плато, накопленным к этому моменту.
const evalAfterPlateauSec = 30;

// Статистики трендов в summary профилей stress, regression и soak.
const summaryTrendStats = ['min', 'avg', 'p(50)', 'p(95)', 'p(99)', 'max'];

// constantRateScenario — сценарий create_order с постоянным RPS (открытая модель).
function constantRateScenario({ rate, duration, preAllocatedVUs, maxVUs }) {
  return {
    create_order: { executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration, preAllocatedVUs, maxVUs },
  };
}

// Калибровка эталона p95 в setup() профиля stress (см. calibrate в create_order.js): прогрев, затем замер,
// оба по времени от начала setup() и в темпе первой ступени.
// Длительность по времени, а не по числу запросов, делает длительность setup() известной заранее: delayAbortEval
// k6 отсчитывается от старта прогона вместе с setup(), и задержка порогов abort сдвигается ровно на setupSec.
export const calibration = {
  // Прогрев: за пару секунд соединения, пулы и кэши сервисов не успевают выйти на рабочий режим,
  // и эталон получается заниженным (ложная деградация на первой ступени).
  warmupSec: 30,
  // Замер: при 25 RPS первой ступени ≈ 1000 запросов.
  measureSec: 40,
  // Запас setupTimeout на loadSeed и последний запрос замера.
  slackSec: 30,
};

// setupSec — длительность setup() профиля stress: калибровка заканчивается не раньше этого срока от начала setup()
// и позже не больше чем на один запрос с паузой темпа.
export const setupSec = calibration.warmupSec + calibration.measureSec;

let stressPlanCache;

// stressRates возвращает целевые RPS ступеней: env STRESS_RATES (список через запятую, для разведки)
// или арифметическая прогрессия start..max с шагом step.
function stressRates() {
  const raw = (__ENV.STRESS_RATES || '').trim();
  if (raw !== '') {
    const rates = raw.split(',').map((v) => Number(v.trim()));
    if (rates.some((r) => !Number.isInteger(r) || r <= 0)) {
      throw new Error(`env STRESS_RATES must be a comma-separated list of positive integers, got "${raw}"`);
    }
    return rates;
  }
  const rates = [];
  for (let r = stress.start; r <= stress.max; r += stress.step) {
    rates.push(r);
  }
  return rates;
}

// stressPlan строит ступени: номер, целевой RPS, границы ступени и плато в секундах от старта сценария.
export function stressPlan() {
  if (stressPlanCache === undefined) {
    const stageSec = stress.rampSec + stress.plateauSec;
    const stages = stressRates().map((rate, i) => ({
      n: i + 1,
      rate,
      startSec: i * stageSec,
      plateauStartSec: i * stageSec + stress.rampSec,
      endSec: (i + 1) * stageSec,
    }));
    stressPlanCache = { stages, rampSec: stress.rampSec, plateauSec: stress.plateauSec, stageSec };
  }
  return stressPlanCache;
}

function stressProfile() {
  const plan = stressPlan();
  const stages = [];
  const thresholds = {
    // Итерация, упавшая исключением, не даёт проверок и не нарушает rate == 1: ловим счётчиком завершённых итераций.
    checked_iterations: ['count>0'],
  };
  for (const s of plan.stages) {
    stages.push({ duration: `${plan.rampSec}s`, target: s.rate });
    stages.push({ duration: `${plan.plateauSec}s`, target: s.rate });

    const plateau = `{stage:${s.n},phase:plateau}`;
    // Оценка через evalAfterPlateauSec после начала плато; delayAbortEval — от старта прогона, то есть с setup().
    const delayAbortEval = `${setupSec + s.plateauStartSec + evalAfterPlateauSec}s`;
    thresholds[`http_req_failed${plateau}`] = [
      { threshold: `rate<${degradation.maxErrorRate}`, abortOnFail: true, delayAbortEval },
    ];
    // p95 > K× эталона ⇔ доля запросов медленнее K× эталона больше 5% (метрика slow, см. create_order.js).
    thresholds[`slow${plateau}`] = [{ threshold: 'rate<0.05', abortOnFail: true, delayAbortEval }];
    // Пустые пороги создают подметрики, которые попадают в summary.
    thresholds[`http_req_duration${plateau}`] = [];
    thresholds[`http_reqs${plateau}`] = [];
    thresholds[`checks${plateau}`] = [];
  }
  // Пропущенные итерации (не хватило VU) — признак, что система не успевает; у них нет тега stage.
  thresholds.dropped_iterations = [{ threshold: 'count==0', abortOnFail: true }];

  return {
    scenarios: {
      create_order: {
        executor: 'ramping-arrival-rate',
        startRate: plan.stages[0].rate,
        timeUnit: '1s',
        preAllocatedVUs: stress.preAllocatedVUs,
        maxVUs: stress.maxVUs,
        stages,
      },
    },
    thresholds,
    // setup() прогревает стек и калибрует эталон p95 (см. create_order.js): дольше дефолтных 60 с.
    setupTimeout: `${setupSec + calibration.slackSec}s`,
    summaryTrendStats,
  };
}

// Пороги регрессионного гейта, выведенные из baseline (load/results/baseline.md, разделы 3 и 6).
// Числа привязаны к машине и лимитам контейнеров baseline; пересматриваются только вместе с новым baseline.
const regression = {
  limitRPS: 375, // предел HTTP: последняя ступень stress без деградации
  loadShare: 0.7, // доля предела, которую держит гейт
  baselineP95Ms: 4.0, // p95 baseline на ступени 275 RPS (ближайшей сверху к 70% предела), мс
  p95Factor: 1.5, // допустимое ухудшение p95
  maxErrorRate: 0.01,
  duration: '5m',
  preAllocatedVUs: 50,
  maxVUs: 200,
};

// regressionRPS: 70% предела, округлено вниз до 5 RPS.
export const regressionRPS = Math.floor((regression.limitRPS * regression.loadShare) / 5) * 5;

// regressionP95Ms возвращает порог p95 в мс: env REGRESSION_P95_MS (проверка, что гейт падает) или 1.5× baseline.
export function regressionP95Ms() {
  const raw = (__ENV.REGRESSION_P95_MS || '').trim();
  if (raw === '') {
    return regression.baselineP95Ms * regression.p95Factor;
  }
  const v = Number(raw);
  if (!(v > 0)) {
    throw new Error(`env REGRESSION_P95_MS must be a positive number, got "${raw}"`);
  }
  return v;
}

// Soak: тот же RPS, что у гейта регрессий (70% предела), 30 мин. Сравниваются первые и последние windowSec секунд.
export const soak = {
  durationSec: 1800,
  windowSec: 300,
  p99Factor: 1.5, // допустимый рост p99 в последнем окне относительно первого
  maxErrorRate: 0.01,
  preAllocatedVUs: 50,
  maxVUs: 200,
};

// soakWindowAt: окно soak (first | middle | last) через elapsedMs после старта сценария.
function soakWindowAt(elapsedMs) {
  if (elapsedMs < soak.windowSec * 1000) {
    return 'first';
  }
  if (elapsedMs >= (soak.durationSec - soak.windowSec) * 1000) {
    return 'last';
  }
  return 'middle';
}

const profiles = {
  // Утечки и деградация во времени: постоянный RPS гейта 30 мин. Пороги ловят только дефекты прогона;
  // дрейф p99 оценивает summary (метрики окон first/last), а не abort: прогон должен дойти до конца.
  soak: () => ({
    scenarios: constantRateScenario({
      rate: regressionRPS,
      duration: `${soak.durationSec / 60}m`,
      preAllocatedVUs: soak.preAllocatedVUs,
      maxVUs: soak.maxVUs,
    }),
    thresholds: {
      http_req_failed: [{ threshold: `rate<${soak.maxErrorRate}`, abortOnFail: true, delayAbortEval: '60s' }],
      checks: [{ threshold: 'rate==1', abortOnFail: true, delayAbortEval: '60s' }],
      checked_iterations: ['count>0'],
      dropped_iterations: ['count==0'],
      // Пустые пороги создают подметрики окон, которые попадают в summary.
      'http_req_duration{window:first}': [],
      'http_req_duration{window:last}': [],
      'http_reqs{window:first}': [],
      'http_reqs{window:last}': [],
      'http_req_failed{window:first}': [],
      'http_req_failed{window:last}': [],
    },
    summaryTrendStats,
  }),
  // Гейт регрессий: постоянный RPS = 70% предела, нарушение любого порога даёт ненулевой код выхода k6.
  regression: () => ({
    scenarios: constantRateScenario({
      rate: regressionRPS,
      duration: regression.duration,
      preAllocatedVUs: regression.preAllocatedVUs,
      maxVUs: regression.maxVUs,
    }),
    thresholds: {
      'http_req_duration{name:create_order}': [`p(95)<=${regressionP95Ms()}`],
      http_req_failed: [`rate<${regression.maxErrorRate}`],
      checks: ['rate==1'],
      checked_iterations: ['count>0'],
      // Пропущенные итерации — система не держит заданный RPS.
      dropped_iterations: ['count==0'],
    },
    summaryTrendStats,
  }),
  // Сценарий и стек работают: 5 RPS × 1 мин. Любая проваленная проверка останавливает прогон.
  smoke: () => ({
    scenarios: constantRateScenario({ rate: 5, duration: '1m', preAllocatedVUs: 10, maxVUs: 50 }),
    thresholds: {
      checks: [{ threshold: 'rate==1', abortOnFail: true }],
      checked_iterations: ['count>0'],
    },
  }),
  stress: stressProfile,
};

// profile возвращает options k6 для профиля name; неизвестный профиль — ошибка, а не молчаливый дефолт.
export function profile(name) {
  const build = profiles[name];
  if (build === undefined) {
    throw new Error(`unknown PROFILE "${name}", expected one of: ${Object.keys(profiles).join(', ')}`);
  }
  return build();
}

// stageAt возвращает ступень, фазу (ramp|plateau) и целевой RPS профиля name через elapsedMs после старта
// сценария. Нужен для тега stage и метрики target_rps (панель «целевой/достигнутый» дашборда Load).
// Считается по собственному плану, а не по options: k6 нормализует options и в VU они уже не в исходной форме.
export function stageAt(name, elapsedMs) {
  if (name === 'stress') {
    const plan = stressPlan();
    const i = Math.min(Math.floor(elapsedMs / (plan.stageSec * 1000)), plan.stages.length - 1);
    const s = plan.stages[i];
    const intoStageMs = Math.max(0, elapsedMs - s.startSec * 1000);
    if (intoStageMs >= plan.rampSec * 1000) {
      return { stage: s.n, phase: 'plateau', target: s.rate };
    }
    const from = i === 0 ? s.rate : plan.stages[i - 1].rate;
    return { stage: s.n, phase: 'ramp', target: from + ((s.rate - from) * intoStageMs) / (plan.rampSec * 1000) };
  }
  const target = profile(name).scenarios.create_order.rate;
  if (name === 'soak') {
    return { stage: 1, phase: 'plateau', target, window: soakWindowAt(elapsedMs) };
  }
  return { stage: 1, phase: 'plateau', target };
}
