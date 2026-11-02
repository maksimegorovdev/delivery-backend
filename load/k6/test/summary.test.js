import './env.js';
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { soak, stressPlan } from '../lib/config.js';
import { buildSummary } from '../lib/summary.js';

const plan = stressPlan();

// stressData — минимальный data handleSummary для stress: сценарий начался startedAgoMs назад,
// ступени из reachedStages имеют подметрики плато, extra добавляет или заменяет метрики.
function stressData({ startedAgoMs, reachedStages, extra = {} }) {
  const metrics = {
    scenario_start_ts: { values: { min: Date.now() - startedAgoMs } },
    iterations: { values: { count: 1000 } },
    checked_iterations: { values: { count: 1000 } },
  };
  for (const n of reachedStages) {
    const tags = `{stage:${n},phase:plateau}`;
    const rate = plan.stages[n - 1].rate;
    metrics[`http_reqs${tags}`] = { values: { count: rate * plan.plateauSec } };
    metrics[`http_req_duration${tags}`] = { values: { 'p(50)': 2, 'p(95)': 4, 'p(99)': 6 } };
    metrics[`http_req_failed${tags}`] = { values: { rate: 0 } };
    metrics[`checks${tags}`] = { values: { rate: 1 } };
    metrics[`slow${tags}`] = { values: { rate: 0 } };
  }
  return { metrics: { ...metrics, ...extra }, state: { testRunDurationMs: startedAgoMs + 60000 } };
}

function stressSummary(data) {
  const out = buildSummary(data, 'stress', 'test');
  return JSON.parse(out['/results/raw/test/summary.json']);
}

test('остановка по dropped_iterations — предел найден, а не дефект прогона', () => {
  const s3 = plan.stages[2];
  const data = stressData({
    // Остановка в середине плато третьей ступени.
    startedAgoMs: (s3.plateauStartSec + 45) * 1000,
    reachedStages: [1, 2, 3],
    extra: { dropped_iterations: { values: { count: 12 }, thresholds: { 'count==0': { ok: false } } } },
  });
  const summary = stressSummary(data);
  assert.equal(summary.verdict, 'limit_reached');
  assert.equal(summary.stopped_in_stage, 3);
  assert.equal(summary.first_degraded_stage, 3);
  assert.equal(summary.limit_rps, plan.stages[1].rate);
});

test('остановка по порогу slow окна оценки — предел найден', () => {
  const s2 = plan.stages[1];
  const data = stressData({
    startedAgoMs: (s2.plateauStartSec + 40) * 1000,
    reachedStages: [1, 2],
    extra: { 'slow{stage:2,eval:1}': { values: { rate: 0.2 }, thresholds: { 'rate<0.05': { ok: false } } } },
  });
  const summary = stressSummary(data);
  assert.equal(summary.verdict, 'limit_reached');
  assert.equal(summary.first_degraded_stage, 2);
  assert.equal(summary.limit_rps, plan.stages[0].rate);
});

test('нарушение постороннего порога — дефект прогона', () => {
  const data = stressData({
    startedAgoMs: (plan.stages[1].plateauStartSec + 40) * 1000,
    reachedStages: [1, 2],
    extra: { checked_iterations: { values: { count: 1000 }, thresholds: { 'count>0': { ok: false } } } },
  });
  assert.equal(stressSummary(data).verdict, 'failed');
});

const dropped = (count) => ({ dropped_iterations: { values: { count }, thresholds: { 'count==0': { ok: false } } } });

test('пропуски на разгоне ступени: она деградировала, предел — предыдущая', () => {
  const s3 = plan.stages[2];
  const data = stressData({
    // Остановка на разгоне третьей ступени: на её плато запросов нет.
    startedAgoMs: (s3.startSec + 20) * 1000,
    reachedStages: [1, 2],
    extra: dropped(5),
  });
  const summary = stressSummary(data);
  assert.equal(summary.verdict, 'limit_reached');
  assert.equal(summary.stopped_in_stage, 3);
  assert.equal(summary.first_degraded_stage, 3);
  assert.equal(summary.limit_rps, plan.stages[1].rate);
});

test('пропуски в первые секунды сценария не роняют summary', () => {
  const data = stressData({ startedAgoMs: 2000, reachedStages: [], extra: dropped(3) });
  const summary = stressSummary(data);
  assert.equal(summary.stopped_in_stage, 1);
  assert.equal(summary.first_degraded_stage, 1);
  assert.equal(summary.limit_rps, null);
});

test('stress без scenario_start_ts (setup упал) — summary пишется с вердиктом failed', () => {
  const data = stressData({ startedAgoMs: 0, reachedStages: [] });
  delete data.metrics.scenario_start_ts;
  data.metrics.iterations.values.count = 0;
  data.metrics.checked_iterations.values.count = 0;
  const summary = stressSummary(data);
  assert.equal(summary.verdict, 'failed');
  assert.equal(summary.scenario_started_at, null);
});

test('stress без подметрик ступеней при пройденных итерациях — дефект прогона, а не «предел не найден»', () => {
  const data = stressData({ startedAgoMs: (plan.stages[2].plateauStartSec + 10) * 1000, reachedStages: [] });
  assert.equal(stressSummary(data).verdict, 'failed');
});

// soakData — data handleSummary для soak: окна first/last с заданным p99 (undefined — нет перцентиля).
function soakData({ firstP99, lastP99, withStart = true }) {
  const metrics = {
    iterations: { values: { count: 1000 } },
    checked_iterations: { values: { count: 1000 } },
  };
  if (withStart) {
    metrics.scenario_start_ts = { values: { min: Date.now() - soak.durationSec * 1000 } };
  }
  for (const [w, p99] of [
    ['first', firstP99],
    ['last', lastP99],
  ]) {
    metrics[`http_reqs{window:${w}}`] = { values: { count: 500 } };
    metrics[`http_req_duration{window:${w}}`] = { values: { 'p(50)': 2, 'p(95)': 4, 'p(99)': p99 } };
    metrics[`http_req_failed{window:${w}}`] = { values: { rate: 0 } };
  }
  return { metrics, state: { testRunDurationMs: soak.durationSec * 1000 } };
}

function soakSummary(data) {
  return JSON.parse(buildSummary(data, 'soak', 'test')['/results/raw/test/summary.json']);
}

test('soak: рост p99 сверх предела — drift, в пределах — completed', () => {
  assert.equal(soakSummary(soakData({ firstP99: 6, lastP99: 6 })).verdict, 'completed');
  assert.equal(soakSummary(soakData({ firstP99: 6, lastP99: 10 })).verdict, 'drift');
});

test('soak: нет p99 у окна — не completed', () => {
  assert.notEqual(soakSummary(soakData({ firstP99: 6, lastP99: undefined })).verdict, 'completed');
  assert.notEqual(soakSummary(soakData({ firstP99: undefined, lastP99: 6 })).verdict, 'completed');
});

test('soak без scenario_start_ts — summary пишется с вердиктом failed', () => {
  const summary = soakSummary(soakData({ firstP99: 6, lastP99: 6, withStart: false }));
  assert.equal(summary.verdict, 'failed');
  assert.equal(summary.scenario_started_at, null);
});
