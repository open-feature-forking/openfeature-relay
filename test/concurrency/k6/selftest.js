// The checker's own test: every check of check.js has to reject a synthetic wrong answer and accept the right one, so a
// green load run means something. Run by TestCheckerSelfTest; each case prints "SELFTEST ok|FAIL <name>".
import { Counter } from 'k6/metrics';
import {
  EventChecker, SyncChecker, checkAnswer, checkBulk, checkMonotonic, checkPayload, expected, keys, newState, same,
} from './check.js';

const failures = new Counter('selftest_failures');
const fixture = JSON.parse(open('/suite/testdata/fixture.json'));

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: { selftest_failures: ['count==0'] },
};

const cfg = { T0: 1000000, P: 3000, D: 1500, EPS: 100, N: 200 };
const at = (g, offset) => cfg.T0 + g * cfg.P + offset;
// A request sent and answered in the middle of generation g's interval sees generation g only.
const mid = (g) => [at(g, 2000), at(g, 2000)];

function docFlags(g) {
  const flags = {};
  for (const k of keys(g)) {
    const w = expected(k, g, '');
    if (k.includes('-tgt-')) {
      flags[k] = { variants: { hit: 'hit', miss: 'miss' }, defaultVariant: 'miss', targeting: { if: [{ ends_with: [{ var: 'targetingKey' }, String(g % 10)] }, 'hit', 'miss'] } };
    } else if (k.includes('-bool-') || k === 'a-odd-only') {
      flags[k] = { variants: { on: true, off: false }, defaultVariant: w.variant };
    } else {
      flags[k] = { variants: { v: w.value }, defaultVariant: w.variant };
    }
  }
  return flags;
}

function bulk(g, selector) {
  return keys(g, selector).map((k) => Object.assign({ key: k }, expected(k, g, 'user-3')));
}

const cases = [];
const test = (name, fn) => cases.push([name, fn]);

test('fixture: check.js agrees with generations.go', () => {
  for (const f of fixture) {
    const got = expected(f.key, f.gen, f.targetingKey);
    if (!same(got, f.want)) throw new Error(`${f.key} gen ${f.gen} ${f.targetingKey}: ${JSON.stringify(got)} != ${JSON.stringify(f.want)}`);
  }
  return fixture.length > 1000;
});

test('accepts the current generation', () => {
  const [s, e] = mid(7);
  return checkAnswer(expected('a-str-3', 7, 'u'), 'a-str-3', 'u', s, e, cfg).ok;
});

test('rejects an answer one generation too old', () => {
  const [s, e] = mid(7);
  const r = checkAnswer(expected('a-str-3', 6, 'u'), 'a-str-3', 'u', s, e, cfg);
  return !r.ok && r.kind === 'stale';
});

test('rejects an answer from the future', () => {
  const [s, e] = mid(7);
  const r = checkAnswer(expected('a-str-3', 8, 'u'), 'a-str-3', 'u', s, e, cfg);
  return !r.ok && r.kind === 'wrong';
});

test('accepts both generations at the window edge', () => {
  const s = at(7, 100);
  const e = at(7, 200);
  return checkAnswer(expected('a-str-3', 6, 'u'), 'a-str-3', 'u', s, e, cfg).ok
    && checkAnswer(expected('a-str-3', 7, 'u'), 'a-str-3', 'u', s, e, cfg).ok
    && !checkAnswer(expected('a-str-3', 5, 'u'), 'a-str-3', 'u', s, e, cfg).ok
    && !checkAnswer(expected('a-str-3', 8, 'u'), 'a-str-3', 'u', s, e, cfg).ok;
});

test('rejects a wrong targeting variant', () => {
  const [s, e] = mid(7);
  const wrong = { found: true, variant: 'miss', value: 'miss' };
  return !checkAnswer(wrong, 'a-tgt-1', 'user-7', s, e, cfg).ok
    && checkAnswer({ found: true, variant: 'hit', value: 'hit' }, 'a-tgt-1', 'user-7', s, e, cfg).ok;
});

test('rejects a-odd-only found in an even generation', () => {
  const [s, e] = mid(8);
  return !checkAnswer({ found: true, variant: 'on', value: true }, 'a-odd-only', 'u', s, e, cfg).ok;
});

test('accepts a 404 for a-odd-only in an even generation', () => {
  const [s, e] = mid(8);
  return checkAnswer({ found: false }, 'a-odd-only', 'u', s, e, cfg).ok;
});

test('rejects a 404 for a flag that exists', () => {
  const [s, e] = mid(8);
  return !checkAnswer({ found: false }, 'a-str-0', 'u', s, e, cfg).ok;
});

test('rejects a backwards gen', () => {
  const state = newState();
  return checkMonotonic(state, 'a-gen', { found: true, value: 9 }).ok
    && !checkMonotonic(state, 'a-gen', { found: true, value: 8 }).ok
    && checkMonotonic(state, 'b-gen', { found: true, value: 3 }).ok;
});

test('accepts a bulk answer of one generation', () => {
  const [s, e] = mid(7);
  return checkBulk(bulk(7), 'user-3', s, e, cfg).ok;
});

test('rejects a bulk answer with two generations', () => {
  const [s, e] = mid(7);
  const entries = bulk(7);
  const old = expected('a-str-0', 6, 'user-3');
  entries[1] = { key: 'a-str-0', found: true, variant: old.variant, value: old.value };
  const first = entries.findIndex((x) => x.key === 'a-str-0');
  entries[first] = { key: 'a-str-0', variant: old.variant, value: old.value };
  return !checkBulk(entries, 'user-3', s, e, cfg).ok;
});

test('rejects a bulk answer of an old generation', () => {
  const [s, e] = mid(7);
  const r = checkBulk(bulk(6), 'user-3', s, e, cfg);
  return !r.ok && r.kind === 'stale';
});

test('rejects a bulk answer missing a-odd-only in an odd generation', () => {
  const [s, e] = mid(7);
  return !checkBulk(bulk(7).filter((x) => x.key !== 'a-odd-only'), 'user-3', s, e, cfg).ok;
});

test('accepts a sync stream that repeats a generation', () => {
  const c = new SyncChecker(cfg, null, 'sync-1');
  const [s, e] = mid(7);
  return c.payload(docFlags(7), s, e).ok && c.payload(docFlags(7), s, e).ok && c.payload(docFlags(8), s, e).ok;
});

test('rejects a sync stream that skips a generation', () => {
  const c = new SyncChecker(cfg, null, 'sync-1');
  const [s, e] = mid(7);
  const r = c.payload(docFlags(7), s, e).ok && c.payload(docFlags(9), s, e);
  return r && !r.ok && r.kind === 'lag';
});

test('rejects a sync stream that goes backwards', () => {
  const c = new SyncChecker(cfg, null, 'sync-1');
  const [s, e] = mid(7);
  return c.payload(docFlags(7), s, e).ok && !c.payload(docFlags(6), s, e).ok;
});

test('rejects a first payload outside the window', () => {
  const c = new SyncChecker(cfg, null, 'sync-1');
  const [s, e] = mid(7);
  return !c.payload(docFlags(5), s, e).ok;
});

test('a stream that ends behind fails its final check', () => {
  const c = new SyncChecker(cfg, null, 'sync-1');
  const [s, e] = mid(7);
  c.payload(docFlags(7), s, e);
  return !c.finish(at(9, 2000)).ok && c.finish(at(7, 2000)).ok;
});

test('rejects a b- key on a selected stream', () => {
  const flags = docFlags(7);
  const a = {};
  for (const k of Object.keys(flags)) if (k.startsWith('a-')) a[k] = flags[k];
  const ok = checkPayload(a, 'a').ok;
  a['b-str-0'] = flags['b-str-0'];
  return ok && !checkPayload(a, 'a').ok;
});

test('rejects a payload that mixes generations', () => {
  const flags = docFlags(7);
  flags['a-str-2'] = docFlags(6)['a-str-2'];
  return !checkPayload(flags, null).ok;
});

test('event stream: one configuration_change per generation written, keep_alive ignored', () => {
  const start = at(2, 500);
  const end = at(8, 500);
  const ok = new EventChecker(cfg, 'ev', start);
  ok.event('provider_ready');
  for (let g = 3; g <= 8; g++) ok.event('configuration_change');
  ok.event('keep_alive');
  const short = new EventChecker(cfg, 'ev', start);
  short.event('provider_ready');
  short.event('configuration_change');
  const first = new EventChecker(cfg, 'ev', start);
  return ok.finish(end).ok && !short.finish(end).ok && !first.event('configuration_change').ok;
});

export default function () {
  for (const [name, fn] of cases) {
    let pass = false;
    try {
      pass = fn() === true;
    } catch (err) {
      console.log(`SELFTEST error ${name}: ${err}`);
    }
    if (!pass) failures.add(1);
    console.log(`SELFTEST ${pass ? 'ok' : 'FAIL'} ${name}`);
  }
}
