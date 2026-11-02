// Профили нагрузки. Выбираются env PROFILE; открытая модель (arrival rate): RPS задаётся,
// а не получается, поэтому медленная система не снижает нагрузку сама себе.

const profiles = {
  // Сценарий и стек работают: 5 RPS × 1 мин. Любая проваленная проверка останавливает прогон.
  smoke: {
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
      checks: [{ threshold: 'rate == 1', abortOnFail: true }],
    },
  },
};

// profile возвращает options k6 для профиля name; неизвестный профиль — ошибка, а не молчаливый дефолт.
export function profile(name) {
  const p = profiles[name];
  if (p === undefined) {
    throw new Error(`unknown PROFILE "${name}", expected one of: ${Object.keys(profiles).join(', ')}`);
  }
  return p;
}

// targetRate возвращает целевой RPS профиля name для метрики target_rps (панель «целевой/достигнутый» дашборда Load).
// Пока профили с постоянной интенсивностью; ступенчатые профили добавят зависимость от времени.
export function targetRate(name) {
  return profile(name).scenarios.create_order.rate;
}
