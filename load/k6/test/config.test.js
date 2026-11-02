import './env.js';
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { profile, stageAt, stressPlan } from '../lib/config.js';

const plan = stressPlan();
const sec = (s) => s * 1000;

test('stageAt stress: окно оценки начинается через evalAfterPlateauSec после начала плато', () => {
  const s = plan.stages[1];
  assert.equal(stageAt('stress', sec(s.startSec + 1)).eval, false, 'разгон');
  assert.equal(stageAt('stress', sec(s.plateauStartSec)).eval, false, 'начало плато');
  assert.equal(stageAt('stress', sec(s.plateauStartSec + 29.999)).eval, false, 'до окна оценки');
  assert.equal(stageAt('stress', sec(s.plateauStartSec + 30)).eval, true, 'начало окна оценки');
  assert.equal(stageAt('stress', sec(s.endSec - 0.001)).eval, true, 'конец плато');
});

test('stageAt вне stress не размечает окно оценки', () => {
  for (const name of ['smoke', 'regression', 'soak']) {
    assert.equal(stageAt(name, sec(100)).eval, undefined, name);
  }
});

test('пороги abort stress не зависят от времени setup()', () => {
  const { thresholds } = profile('stress');
  for (const s of plan.stages) {
    for (const name of [`http_req_failed{stage:${s.n},eval:1}`, `slow{stage:${s.n},eval:1}`]) {
      const [t] = thresholds[name] || [];
      assert.ok(t, `нет порога ${name}`);
      assert.equal(t.abortOnFail, true, name);
      // delayAbortEval отсчитывается от старта прогона вместе с setup(): окно задаёт тег eval, а не задержка.
      assert.equal(t.delayAbortEval, undefined, name);
    }
    // Статистика плато для summary — по всему плато.
    assert.deepEqual(thresholds[`http_req_duration{stage:${s.n},phase:plateau}`], []);
  }
});
