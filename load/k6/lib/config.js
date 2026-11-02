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

// Через сколько секунд плато начинать оценивать пороги: первые секунды выборка слишком мала.
const evalAfterPlateauSec = 30;

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
    // Оценка начинается через evalAfterPlateauSec после начала плато ступени (время от старта теста).
    const delayAbortEval = `${s.plateauStartSec + evalAfterPlateauSec}s`;
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
    setupTimeout: '180s',
    summaryTrendStats: ['min', 'avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
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

const profiles = {
  // Гейт регрессий: постоянный RPS = 70% предела, нарушение любого порога даёт ненулевой код выхода k6.
  regression: () => ({
    scenarios: {
      create_order: {
        executor: 'constant-arrival-rate',
        rate: regressionRPS,
        timeUnit: '1s',
        duration: regression.duration,
        preAllocatedVUs: regression.preAllocatedVUs,
        maxVUs: regression.maxVUs,
      },
    },
    thresholds: {
      'http_req_duration{name:create_order}': [`p(95)<=${regressionP95Ms()}`],
      http_req_failed: [`rate<${regression.maxErrorRate}`],
      checks: ['rate==1'],
      checked_iterations: ['count>0'],
      // Пропущенные итерации — система не держит заданный RPS.
      dropped_iterations: ['count==0'],
    },
    summaryTrendStats: ['min', 'avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
  }),
  // Сценарий и стек работают: 5 RPS × 1 мин. Любая проваленная проверка останавливает прогон.
  smoke: () => ({
    scenarios: {
      create_order: {
        executor: 'constant-arrival-rate',
        rate: 5,
        timeUnit: '1s',
        duration: '1m',
        preAllocatedVUs: 10,
        maxVUs: 50,
      },
    },
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
  return { stage: 1, phase: 'plateau', target: profile(name).scenarios.create_order.rate };
}
