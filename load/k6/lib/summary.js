import { degradation, regressionRPS, soak, stressPlan } from './config.js';

// Пороги, нарушение которых означает «предел найден»; любое другое нарушение — дефект прогона.
const limitThresholds = [/^http_req_failed\{stage:/, /^slow\{stage:/, /^dropped_iterations$/];

// Запас на время между остановкой сценария и вызовом handleSummary при определении ступени остановки.
const stopSlackMs = 5000;

function metric(data, name) {
  return data.metrics[name];
}

function value(data, name, key) {
  const m = metric(data, name);
  return m === undefined || m.values === undefined ? undefined : m.values[key];
}

function iso(ms) {
  return new Date(ms).toISOString();
}

function failedThresholds(data) {
  const failed = [];
  for (const [name, m] of Object.entries(data.metrics)) {
    for (const [expr, res] of Object.entries(m.thresholds || {})) {
      if (!res.ok) {
        failed.push(`${name}: ${expr}`);
      }
    }
  }
  return failed;
}

// stageStats собирает метрики плато ступени из подметрик, созданных порогами профиля stress.
function stageStats(data, s, plan, scenarioStartMs, elapsedMs) {
  const tags = `{stage:${s.n},phase:plateau}`;
  const requests = value(data, `http_reqs${tags}`, 'count') || 0;
  const plateauElapsedSec = Math.min(plan.plateauSec, elapsedMs / 1000 - s.plateauStartSec);
  const partial = requests > 0 && plateauElapsedSec < plan.plateauSec - stopSlackMs / 1000;
  const achievedRPS = requests > 0 && plateauElapsedSec > 0 ? requests / plateauElapsedSec : 0;
  const checksRate = value(data, `checks${tags}`, 'rate');
  return {
    stage: s.n,
    target_rps: s.rate,
    reached: requests > 0,
    partial,
    started_at: iso(scenarioStartMs + s.startSec * 1000),
    plateau_started_at: iso(scenarioStartMs + s.plateauStartSec * 1000),
    ended_at: iso(scenarioStartMs + s.endSec * 1000),
    requests,
    achieved_rps: Number(achievedRPS.toFixed(1)),
    achieved_share: Number((achievedRPS / s.rate).toFixed(3)),
    p50_ms: value(data, `http_req_duration${tags}`, 'p(50)'),
    p95_ms: value(data, `http_req_duration${tags}`, 'p(95)'),
    p99_ms: value(data, `http_req_duration${tags}`, 'p(99)'),
    error_rate: value(data, `http_req_failed${tags}`, 'rate'),
    check_fail_rate: checksRate === undefined ? undefined : Number((1 - checksRate).toFixed(4)),
    slow_share: value(data, `slow${tags}`, 'rate'),
  };
}

// markDegradation применяет критерий предела к собранным ступеням (то же, что пороги, но с точными числами).
function markDegradation(stages, stoppedStage) {
  const first = stages.find((s) => s.reached);
  for (const s of stages) {
    s.reasons = [];
    if (!s.reached) {
      continue;
    }
    s.p95_ratio_to_first = first.p95_ms > 0 ? Number((s.p95_ms / first.p95_ms).toFixed(2)) : undefined;
    if (s.error_rate > degradation.maxErrorRate) {
      s.reasons.push(`error_rate ${s.error_rate} > ${degradation.maxErrorRate}`);
    }
    if (s.p95_ratio_to_first > degradation.p95Factor) {
      s.reasons.push(`p95 ${s.p95_ratio_to_first}x of first stage > ${degradation.p95Factor}x`);
    }
    if (!s.partial && s.achieved_share < degradation.minRPSShare) {
      s.reasons.push(`achieved ${s.achieved_share} of target < ${degradation.minRPSShare}`);
    }
    if (s.stage === stoppedStage) {
      s.reasons.push('dropped_iterations');
    }
    s.degraded = s.reasons.length > 0;
  }
}

// markThresholdStages помечает деградировавшими ступени, на которых сработал порог abortOnFail: пороги и
// пост-расчёт оценивают одно и то же по-разному (приближение p95 долей slow), остановку определяют пороги.
function markThresholdStages(stages, failed) {
  for (const f of failed) {
    const m = /^(?:http_req_failed|slow)\{stage:(\d+),/.exec(f);
    const s = m === null ? undefined : stages.find((x) => x.stage === Number(m[1]));
    if (s !== undefined) {
      s.reasons.push(`threshold ${f}`);
      s.degraded = true;
    }
  }
}

function limitOf(stages) {
  const firstDegraded = stages.find((s) => s.degraded);
  if (firstDegraded === undefined) {
    return { first_degraded_stage: null, last_good_stage: null, limit_rps: null };
  }
  const lastGood = stages.filter((s) => s.reached && s.stage < firstDegraded.stage).pop();
  return {
    first_degraded_stage: firstDegraded.stage,
    last_good_stage: lastGood === undefined ? null : lastGood.stage,
    limit_rps: lastGood === undefined ? null : lastGood.target_rps,
  };
}

// soakWindowStats: метрики окна soak (first|last). from/to — абсолютные границы окна в миллисекундах.
function soakWindowStats(data, name, fromMs, toMs) {
  const tags = `{window:${name}}`;
  const requests = value(data, `http_reqs${tags}`, 'count') || 0;
  return {
    window: name,
    from: iso(fromMs),
    to: iso(toMs),
    requests,
    p50_ms: value(data, `http_req_duration${tags}`, 'p(50)'),
    p95_ms: value(data, `http_req_duration${tags}`, 'p(95)'),
    p99_ms: value(data, `http_req_duration${tags}`, 'p(99)'),
    error_rate: value(data, `http_req_failed${tags}`, 'rate'),
  };
}

// soakSummary сравнивает первое и последнее окно прогона. Ступени stages[] (1 = первое окно, 2 = последнее)
// повторяют формат stress, чтобы sql/lag.sql считал лаг по окнам без изменений.
function soakSummary(data, runID, failed) {
  const startMs = value(data, 'scenario_start_ts', 'min');
  const windowMs = soak.windowSec * 1000;
  const endMs = startMs + soak.durationSec * 1000;
  const first = soakWindowStats(data, 'first', startMs, startMs + windowMs);
  const last = soakWindowStats(data, 'last', endMs - windowMs, endMs);
  const ratio = first.p99_ms > 0 ? Number((last.p99_ms / first.p99_ms).toFixed(2)) : undefined;

  const iterations = value(data, 'iterations', 'count') || 0;
  const checked = value(data, 'checked_iterations', 'count') || 0;
  let verdict = 'completed';
  if (failed.length > 0 || checked === 0 || checked < iterations || first.requests === 0 || last.requests === 0) {
    verdict = 'failed';
  } else if (ratio === undefined || ratio > soak.p99Factor) {
    verdict = 'drift';
  }
  const stages = [first, last].map((w, i) => ({
    stage: i + 1,
    window: w.window,
    target_rps: regressionRPS,
    reached: w.requests > 0,
    plateau_started_at: w.from,
    ended_at: w.to,
    requests: w.requests,
    p50_ms: w.p50_ms,
    p95_ms: w.p95_ms,
    p99_ms: w.p99_ms,
    error_rate: w.error_rate,
  }));
  return {
    run: runID,
    profile: 'soak',
    verdict,
    scenario_started_at: iso(startMs),
    finished_at: iso(Date.now()),
    test_run_duration_ms: Math.round(data.state.testRunDurationMs),
    target_rps: regressionRPS,
    iterations,
    checked_iterations: checked,
    dropped_iterations: value(data, 'dropped_iterations', 'count') || 0,
    failed_thresholds: failed,
    p99_factor_limit: soak.p99Factor,
    p99_ratio_last_to_first: ratio,
    stages,
  };
}

function pad(v, w) {
  return String(v === undefined ? '—' : v).padStart(w);
}

function stagesTable(stages) {
  const lines = ['stage  target  achieved  p50ms  p95ms  p99ms  errors  degraded'];
  for (const s of stages.filter((x) => x.reached)) {
    const f = (v) => (v === undefined ? undefined : Number(v.toFixed(1)));
    lines.push(
      `${pad(s.stage, 5)}  ${pad(s.target_rps, 6)}  ${pad(s.achieved_rps, 8)}  ${pad(f(s.p50_ms), 5)}  ` +
        `${pad(f(s.p95_ms), 5)}  ${pad(f(s.p99_ms), 5)}  ${pad(s.error_rate, 6)}  ` +
        `${s.degraded ? 'YES: ' + s.reasons.join('; ') : 'no'}`,
    );
  }
  return lines;
}

// buildSummary формирует stdout для любого профиля и, для stress, summary.json с метриками по ступеням,
// границами ступеней и пределом. Файл пишется в /results/raw/<run>/ (каталог смонтирован из load/results).
export function buildSummary(data, profileName, runID) {
  const failed = failedThresholds(data);
  const lines = [
    `profile=${profileName} duration=${Math.round(data.state.testRunDurationMs / 1000)}s`,
    `requests=${value(data, 'http_reqs', 'count')} rps=${Number((value(data, 'http_reqs', 'rate') || 0).toFixed(1))}` +
      ` p95=${Number((value(data, 'http_req_duration', 'p(95)') || 0).toFixed(1))}ms` +
      ` checks=${Number((100 * (value(data, 'checks', 'rate') || 0)).toFixed(2))}%` +
      ` dropped=${value(data, 'dropped_iterations', 'count') || 0}`,
  ];
  if (failed.length > 0) {
    lines.push('thresholds crossed:', ...failed.map((f) => `  ${f}`));
  }

  const result = {};
  if (profileName === 'stress') {
    const plan = stressPlan();
    const scenarioStartMs = value(data, 'scenario_start_ts', 'min');
    const finishedMs = Date.now();
    const elapsedMs = finishedMs - scenarioStartMs;
    const dropped = value(data, 'dropped_iterations', 'count') || 0;
    // Пропущенные итерации не несут тега stage: ступень остановки определяется по времени.
    const stoppedStage =
      dropped > 0
        ? plan.stages.filter((s) => s.startSec * 1000 <= elapsedMs - stopSlackMs).pop().n
        : undefined;

    const stages = plan.stages.map((s) => stageStats(data, s, plan, scenarioStartMs, elapsedMs));
    markDegradation(stages, stoppedStage);
    markThresholdStages(stages, failed);

    const unexpected = failed.filter((f) => !limitThresholds.some((re) => re.test(f)));
    const limitCrossed = failed.length > unexpected.length;
    // Итерации, не дошедшие до проверок, упали исключением в сценарии: результат ступеней недостоверен.
    const iterations = value(data, 'iterations', 'count') || 0;
    const checked = value(data, 'checked_iterations', 'count') || 0;
    let verdict = 'completed';
    if (unexpected.length > 0 || checked === 0 || checked < iterations) {
      verdict = 'failed';
    } else if (limitCrossed) {
      verdict = 'limit_reached';
    }

    const summary = {
      run: runID,
      profile: profileName,
      verdict,
      scenario_started_at: iso(scenarioStartMs),
      finished_at: iso(finishedMs),
      test_run_duration_ms: Math.round(data.state.testRunDurationMs),
      baseline_p95_ms: value(data, 'baseline_p95_ms', 'value'),
      iterations,
      checked_iterations: checked,
      dropped_iterations: dropped,
      stopped_in_stage: stoppedStage === undefined ? null : stoppedStage,
      failed_thresholds: failed,
      criteria: degradation,
      ...limitOf(stages),
      stages,
    };
    result[`/results/raw/${runID}/summary.json`] = JSON.stringify(summary, null, 2);
    lines.push(`verdict=${verdict} limit_rps=${summary.limit_rps} first_degraded_stage=${summary.first_degraded_stage}`);
    lines.push(...stagesTable(stages));
  }
  if (profileName === 'soak') {
    const summary = soakSummary(data, runID, failed);
    result[`/results/raw/${runID}/summary.json`] = JSON.stringify(summary, null, 2);
    lines.push(`verdict=${summary.verdict} p99_last/first=${summary.p99_ratio_last_to_first} (limit ${soak.p99Factor})`);
    const ms = (v) => (v === undefined ? '—' : `${v.toFixed(1)}ms`);
    for (const s of summary.stages) {
      lines.push(
        `${s.window}: requests=${s.requests} p50=${ms(s.p50_ms)} p95=${ms(s.p95_ms)} p99=${ms(s.p99_ms)} errors=${s.error_rate}`,
      );
    }
  }
  result.stdout = lines.join('\n') + '\n';
  return result;
}
