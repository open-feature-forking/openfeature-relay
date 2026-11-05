// The concurrency load (NFR-017): OFREP, flag evaluation and flag sync clients against flagd-minimal while the
// conductor changes its flags. Every answer is checked against the window of check.js; k6 exits 99 when any counter below
// leaves zero.
//
// Environment (set by the harness): HOST, EVAL_PORT, OFREP_PORT, SYNC_PORT, PROTOSET, T0, P, D, EPS (ms), N (the last
// generation written), END (ms since the epoch, when the run ends), PROFILE.
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import exec from 'k6/execution';
import { Client, Stream, StatusOK, StatusNotFound } from 'k6/net/grpc';
import {
  EventChecker, SyncChecker, checkAnswer, checkBulk, checkMonotonic, checkPayload, newState, windowOf,
} from './check.js';

const cfg = {
  T0: Number(__ENV.T0), P: Number(__ENV.P), D: Number(__ENV.D), EPS: Number(__ENV.EPS), N: Number(__ENV.N),
};
const END = Number(__ENV.END);
const HOST = __ENV.HOST;
const OFREP = `http://${HOST}:${__ENV.OFREP_PORT}/ofrep/v1`;
const EVAL = `${HOST}:${__ENV.EVAL_PORT}`;
const SYNC = `${HOST}:${__ENV.SYNC_PORT}`;
const CHURN_MS = Number(__ENV.CHURN_MS || 10000);

const wrongAnswer = new Counter('wrong_answer');
const staleAnswer = new Counter('stale_answer');
const streamLag = new Counter('stream_lag');
const streamError = new Counter('stream_error');
const httpError = new Counter('http_error');
const grpcError = new Counter('grpc_error');
const wideWindow = new Counter('wide_window');
const answers = new Counter('answers_checked');
const scriptError = new Counter('script_error');

// A profile sets each scenario's VUs and arrival rate. "one" is the harness's own: one VU per scenario at a low rate.
// The rates of smoke and full sit below saturation: about 75 requests a second per flagd core under -race (see the PR
// description); the goal is concurrency, not saturation.
const PROFILES = {
  smoke: {
    ofrepSingle: { rate: 10, vus: 2 }, ofrepBulk: { rate: 2, vus: 2 }, rpcUnary: { rate: 10, vus: 3 },
    rpcEvents: { vus: 1 }, syncHeld: { vus: 2 }, syncChurn: { vus: 2 },
  },
  full: {
    ofrepSingle: { rate: 100, vus: 30 }, ofrepBulk: { rate: 10, vus: 10 }, rpcUnary: { rate: 100, vus: 30 },
    rpcEvents: { vus: 4 }, syncHeld: { vus: 40 }, syncChurn: { vus: 10 },
  },
  one: {
    ofrepSingle: { rate: 20, vus: 1 }, ofrepBulk: { rate: 5, vus: 1 }, rpcUnary: { rate: 20, vus: 1 },
    rpcEvents: { vus: 1 }, syncHeld: { vus: 1 }, syncChurn: { vus: 1 },
  },
};
// "measure" is TestMeasureRates: the request scenarios at RATE per second (bulk at a tenth) with enough VUs.
const RATE = Number(__ENV.RATE || 0);
PROFILES.measure = {
  ofrepSingle: { rate: RATE, vus: Math.ceil(RATE / 50) + 2 }, ofrepBulk: { rate: Math.max(1, RATE / 10), vus: Math.ceil(RATE / 100) + 2 },
  rpcUnary: { rate: RATE, vus: Math.ceil(RATE / 50) + 2 }, rpcEvents: { vus: 1 }, syncHeld: { vus: 1 }, syncChurn: { vus: 1 },
};
const profile = PROFILES[__ENV.PROFILE || 'one'];

const seconds = Math.max(1, Math.ceil((END - Date.now()) / 1000));
const arrival = (p, name) => ({
  executor: 'constant-arrival-rate', rate: p.rate, timeUnit: '1s', duration: `${seconds}s`,
  preAllocatedVUs: p.vus, maxVUs: 2 * p.vus, exec: name,
});
const held = (p, name) => ({
  executor: 'per-vu-iterations', vus: p.vus, iterations: 1, maxDuration: `${seconds + 30}s`, exec: name,
});

export const options = {
  scenarios: {
    'ofrep-single': arrival(profile.ofrepSingle, 'ofrepSingle'),
    'ofrep-bulk': arrival(profile.ofrepBulk, 'ofrepBulk'),
    'rpc-unary': arrival(profile.rpcUnary, 'rpcUnary'),
    'rpc-events': held(profile.rpcEvents, 'rpcEvents'),
    'sync-held': held(profile.syncHeld, 'syncHeld'),
    'sync-churn': held(profile.syncChurn, 'syncChurn'),
  },
  thresholds: {
    checks: ['rate==1'],
    wrong_answer: ['count==0'],
    stale_answer: ['count==0'],
    stream_lag: ['count==0'],
    stream_error: ['count==0'],
    http_error: ['count==0'],
    grpc_error: ['count==0'],
    script_error: ['count==0'],
  },
};

// loadProtoset runs in the init context only, so every connection a VU opens needs its client created here.
function loaded() {
  const c = new Client();
  c.loadProtoset(__ENV.PROTOSET);
  return c;
}
const unaryClient = loaded();
const heldClient = loaded();
const churnClient = loaded();
const eventClients = [loaded(), loaded(), loaded()];

// guard counts an exception in a scenario as script_error: k6 does not fail a run for one.
function guard(name, fn) {
  return async () => {
    try {
      await fn();
    } catch (err) {
      scriptError.add(1);
      console.error(`${name}: ${err}`);
    }
  };
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const state = newState();
let reported = 0;

// record counts one check result; the first failures of a VU are printed with their reason.
function record(r, what, tags) {
  answers.add(1);
  check(r, { [what]: (x) => x.ok }, tags);
  if (r.ok) {
    if (r.wide) wideWindow.add(1);
    return r.ok;
  }
  const counter = { stale: staleAnswer, lag: streamLag }[r.kind] || wrongAnswer;
  counter.add(1);
  if (reported++ < 20) console.error(`${what}: ${r.kind}: ${r.reason}`);
  return false;
}

function fail(counter, what, detail) {
  counter.add(1);
  check(null, { [what]: () => false });
  if (reported++ < 20) console.error(`${what}: ${detail}`);
}

const KINDS = ['gen', 'str', 'bool', 'flt', 'tgt', 'obj'];
const rand = (n) => Math.floor(Math.random() * n);
function pick() {
  const r = rand(100);
  if (r === 0) return 'a-odd-only';
  const set = rand(2) === 0 ? 'a' : 'b';
  const kind = KINDS[rand(KINDS.length)];
  return kind === 'gen' ? `${set}-gen` : `${set}-${kind}-${rand(10)}`;
}
const targetingKey = () => `user-${rand(100)}`;

// ---- OFREP ----

const ofrepParams = { headers: { 'Content-Type': 'application/json' }, responseCallback: http.expectedStatuses(200, 404) };

function ofrepAnswer(body) {
  return { found: true, variant: body.variant, value: body.value };
}

export const ofrepSingle = guard('ofrep-single', () => {
  const key = pick();
  const tk = targetingKey();
  const s = Date.now();
  const res = http.post(`${OFREP}/evaluate/flags/${key}`, JSON.stringify({ context: { targetingKey: tk } }), Object.assign({ tags: { name: 'ofrep-single' } }, ofrepParams));
  const e = Date.now();
  let body;
  try { body = res.json(); } catch (err) { body = null; }
  if (res.status === 404 && body && body.errorCode === 'FLAG_NOT_FOUND') {
    record(checkAnswer({ found: false }, key, tk, s, e, cfg), 'ofrep-single answer in window');
  } else if (res.status === 200 && body) {
    const answer = ofrepAnswer(body);
    record(checkAnswer(answer, key, tk, s, e, cfg), 'ofrep-single answer in window');
    record(checkMonotonic(state, key, answer), 'ofrep-single gen never goes backwards');
  } else {
    fail(httpError, 'ofrep-single status', `${key}: status ${res.status} ${res.body}`);
  }
});

export const ofrepBulk = guard('ofrep-bulk', () => {
  const tk = targetingKey();
  const s = Date.now();
  const res = http.post(`${OFREP}/evaluate/flags`, JSON.stringify({ context: { targetingKey: tk } }), Object.assign({ tags: { name: 'ofrep-bulk' } }, ofrepParams));
  const e = Date.now();
  if (res.status !== 200) {
    fail(httpError, 'ofrep-bulk status', `status ${res.status} ${res.body}`);
    return;
  }
  const entries = res.json().flags.filter((f) => !f.errorCode);
  record(checkBulk(entries, tk, s, e, cfg, null), 'ofrep-bulk one generation in window');
});

// ---- RPC unary ----

const SERVICES = ['flagd.evaluation.v1.Service', 'flagd.evaluation.v2.Service', 'schema.v1.Service'];
const METHOD = { odd: 'ResolveBoolean',  gen: 'ResolveInt', str: 'ResolveString', bool: 'ResolveBoolean', flt: 'ResolveFloat', tgt: 'ResolveString', obj: 'ResolveObject' };
let unaryConnected = false;
let rotation = 0;

function anyValue(f) {
  for (const k of ['boolValue', 'stringValue', 'doubleValue', 'objectValue']) if (f[k] !== undefined) return f[k];
  return undefined;
}

export const rpcUnary = guard('rpc-unary', () => {
  if (!unaryConnected) {
    unaryClient.connect(EVAL, { plaintext: true });
    unaryConnected = true;
  }
  const tk = targetingKey();
  const context = { targetingKey: tk };
  const service = SERVICES[rotation++ % SERVICES.length];
  const all = rotation % 5 === 0 && service !== 'flagd.evaluation.v2.Service';
  const s = Date.now();
  if (all) {
    const res = unaryClient.invoke(`${service}/ResolveAll`, { context });
    const e = Date.now();
    if (res.status !== StatusOK) {
      fail(grpcError, 'rpc ResolveAll status', `${service} status ${res.status}`);
      return;
    }
    const entries = Object.entries(res.message.flags || {}).map(([key, f]) => ({ key, found: true, variant: f.variant, value: anyValue(f) }));
    record(checkBulk(entries, tk, s, e, cfg, null), 'rpc ResolveAll one generation in window');
    return;
  }
  const key = pick();
  const method = METHOD[key.split('-')[1]];
  const res = unaryClient.invoke(`${service}/${method}`, { flagKey: key, context });
  const e = Date.now();
  if (res.status === StatusNotFound) {
    record(checkAnswer({ found: false }, key, tk, s, e, cfg), 'rpc answer in window');
  } else if (res.status === StatusOK) {
    let value = res.message.value;
    if (method === 'ResolveInt') value = Number(value);
    const answer = { found: true, variant: res.message.variant, value };
    record(checkAnswer(answer, key, tk, s, e, cfg), 'rpc answer in window');
    record(checkMonotonic(state, key, answer), 'rpc gen never goes backwards');
  } else {
    fail(grpcError, 'rpc status', `${service}/${method} ${key}: status ${res.status}`);
  }
});

// ---- held streams ----

function payloadFlags(message) {
  return JSON.parse(message.flagConfiguration).flags;
}

// planned close: client.close() fires error ("canceled by client") and then end, so a close the scenario asked for
// is told apart from a stream that dropped.
function watch(stream, name, onData) {
  const st = { closing: false };
  stream.on('data', onData);
  stream.on('error', (e) => { if (!st.closing) fail(streamError, 'stream error', `${name}: ${e && e.message ? e.message : e}`); });
  stream.on('end', () => { if (!st.closing) fail(streamError, 'stream end', `${name}: ended before the run did`); });
  return st;
}

export const syncHeld = guard('sync-held', async () => {
  const vu = exec.vu.idInTest;
  const set = vu % 2 === 0 ? 'a' : null;
  const name = `sync-held-${vu}${set ? '-' + set : ''}`;
  const chk = new SyncChecker(cfg, set, name);
  heldClient.connect(SYNC, { plaintext: true });
  const stream = new Stream(heldClient, '/flagd.sync.v1.FlagSyncService/SyncFlags', null);
  const opened = Date.now();
  const st = watch(stream, name, (d) => record(chk.payload(payloadFlags(d), opened, Date.now()), 'sync-held payload'));
  stream.write(set ? { selector: `flagSetId=${set}` } : {});
  await sleep(END - cfg.D - Date.now());
  record(chk.finish(Date.now()), 'sync-held reached the generation due');
  st.closing = true;
  heldClient.close();
});

export const syncChurn = guard('sync-churn', async () => {
  const vu = exec.vu.idInTest;
  let round = 0;
  while (Date.now() + CHURN_MS + cfg.D < END) {
    const set = round % 2 === 0 ? 'a' : null;
    const name = `sync-churn-${vu}-${round}`;
    const chk = new SyncChecker(cfg, set, name);
    churnClient.connect(SYNC, { plaintext: true });
    const stream = new Stream(churnClient, '/flagd.sync.v1.FlagSyncService/SyncFlags', null);
    const opened = Date.now();
    const st = watch(stream, name, (d) => record(chk.payload(payloadFlags(d), opened, Date.now()), 'sync-churn payload'));
    stream.write(set ? { selector: `flagSetId=${set}` } : {});
    await sleep(CHURN_MS);
    st.closing = true;
    churnClient.close();
    await sleep(100);

    churnClient.connect(SYNC, { plaintext: true });
    const s = Date.now();
    const res = churnClient.invoke('flagd.sync.v1.FlagSyncService/FetchAllFlags', set ? { selector: `flagSetId=${set}` } : {});
    const e = Date.now();
    if (res.status !== StatusOK) {
      fail(grpcError, 'FetchAllFlags status', `${name}: status ${res.status}`);
    } else {
      const flags = payloadFlags(res.message);
      const p = checkPayload(flags, set);
      const w = windowOf(s, e, cfg);
      record(p.ok && (p.gen < w.lo || p.gen > w.hi) ? { ok: false, kind: p.gen < w.lo ? 'stale' : 'wrong', reason: `${name}: FetchAllFlags generation ${p.gen}, window ${w.lo}..${w.hi}` } : p, 'FetchAllFlags in window');
    }
    churnClient.close();
    round++;
  }
});

export const rpcEvents = guard('rpc-events', async () => {
  const connectedAt = Date.now();
  const checkers = [];
  const states = [];
  for (const [i, service] of SERVICES.entries()) {
    const c = eventClients[i];
    c.connect(EVAL, { plaintext: true });
    const chk = new EventChecker(cfg, `${service}/EventStream vu${exec.vu.idInTest}`, connectedAt);
    const stream = new Stream(c, `/${service}/EventStream`, null);
    states.push(watch(stream, chk.name, (d) => record(chk.event(d.type), 'event stream order')));
    stream.write({});
    checkers.push(chk);
  }
  await sleep(END - cfg.D - Date.now());
  for (const chk of checkers) record(chk.finish(Date.now()), 'event stream saw every generation');
  states.forEach((st) => { st.closing = true; });
  eventClients.forEach((c) => c.close());
});
