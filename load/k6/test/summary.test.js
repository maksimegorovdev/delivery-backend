import './env.js';
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { stressPlan } from '../lib/config.js';
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
