import './env.js';
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { calibration, profile, setupSec, stageAt, stressPlan } from '../lib/config.js';

const plan = stressPlan();
const sec = (s) => s * 1000;

test('stageAt stress: разгон и плато ступени', () => {
  const s = plan.stages[1];
  assert.deepEqual(stageAt('stress', sec(s.startSec)), { stage: 2, phase: 'ramp', target: plan.stages[0].rate });
  assert.equal(stageAt('stress', sec(s.plateauStartSec - 0.001)).phase, 'ramp');
  assert.deepEqual(stageAt('stress', sec(s.plateauStartSec)), { stage: 2, phase: 'plateau', target: s.rate });
  assert.equal(stageAt('stress', sec(s.endSec)).stage, 3);
});

test('setup() stress длится setupSec: калибровка по времени, setupTimeout с запасом', () => {
  assert.equal(setupSec, calibration.warmupSec + calibration.measureSec);
  assert.equal(profile('stress').setupTimeout, `${setupSec + calibration.slackSec}s`);
});

test('пороги abort stress оцениваются через 30 с плато с учётом setup()', () => {
  const { thresholds } = profile('stress');
  for (const s of plan.stages) {
    // delayAbortEval отсчитывается от старта прогона вместе с setup().
    const delay = `${setupSec + s.plateauStartSec + 30}s`;
    for (const name of [`http_req_failed{stage:${s.n},phase:plateau}`, `slow{stage:${s.n},phase:plateau}`]) {
      const [t] = thresholds[name] || [];
      assert.ok(t, `нет порога ${name}`);
      assert.equal(t.abortOnFail, true, name);
      assert.equal(t.delayAbortEval, delay, name);
    }
  }
});
