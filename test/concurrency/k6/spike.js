// The spike behind TestStreamSpike (Q1): can k6 core hold a flag sync stream for a run, parse its payloads, reconnect and
// open the event streams? Every scenario prints SPIKE lines the Go test reads.
import { Client, Stream } from 'k6/net/grpc';

const host = __ENV.HOST;
const SECONDS = Number(__ENV.SPIKE_SECONDS || 20);
const D = Number(__ENV.D || 1500);
const T0 = Number(__ENV.T0);

// loadProtoset runs in the init context only, so every connection a VU opens needs its client created here.
function loaded() {
  const c = new Client();
  c.loadProtoset(__ENV.PROTOSET);
  return c;
}
const heldClient = loaded();
const churnClient = loaded();
const eventClients = [loaded(), loaded(), loaded()];

export const options = {
  scenarios: {
    held: { executor: 'per-vu-iterations', vus: 1, iterations: 1, maxDuration: `${SECONDS + 30}s`, exec: 'held' },
    churn: { executor: 'per-vu-iterations', vus: 1, iterations: 1, maxDuration: `${SECONDS + 30}s`, exec: 'churn' },
    events: { executor: 'per-vu-iterations', vus: 1, iterations: 1, maxDuration: `${SECONDS + 30}s`, exec: 'events' },
  },
};

function say(scenario, what, extra) {
  console.log('SPIKE ' + JSON.stringify(Object.assign({ scenario, what, at: Date.now() }, extra || {})));
}

function generation(payload) {
  return JSON.parse(payload.flagConfiguration).flags['a-gen'].variants.v;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export async function held() {
  const c = heldClient;
  c.connect(`${host}:${__ENV.SYNC_PORT}`, { plaintext: true });
  const stream = new Stream(c, '/flagd.sync.v1.FlagSyncService/SyncFlags', null);
  let count = 0, last = -1;
  stream.on('data', (d) => { count++; last = generation(d); });
  stream.on('error', (e) => say('held', 'error', { message: String(e && e.message || e) }));
  stream.on('end', () => say('held', 'end'));
  stream.write({});
  const end = Date.now() + SECONDS * 1000;
  await sleep(end - D - Date.now());
  say('held', 'final', { payloads: count, last, expectedAtLeast: Math.floor((Date.now() - T0 - D) / Number(__ENV.P)) });
  c.close();
}

export async function churn() {
  let events = [];
  for (let round = 0; round < 2; round++) {
    const c = churnClient;
    c.connect(`${host}:${__ENV.SYNC_PORT}`, { plaintext: true });
    const stream = new Stream(c, '/flagd.sync.v1.FlagSyncService/SyncFlags', null);
    let first = null;
    stream.on('data', (d) => { if (first === null) first = generation(d); });
    stream.on('error', (e) => events.push('error'));
    stream.on('end', () => events.push('end'));
    stream.write({ selector: 'flagSetId=a' });
    await sleep(3000);
    say('churn', 'round', { round, first });
    c.close();
    await sleep(500);
    say('churn', 'closed', { round, events: events.slice() });
  }
  const c = churnClient;
  c.connect(`${host}:${__ENV.SYNC_PORT}`, { plaintext: true });
  const res = c.invoke('flagd.sync.v1.FlagSyncService/FetchAllFlags', {});
  say('churn', 'fetch', { status: res.status, gen: res.message ? generation(res.message) : null });
  c.close();
}

export async function events() {
  const services = ['flagd.evaluation.v1.Service', 'flagd.evaluation.v2.Service', 'schema.v1.Service'];
  const clients = [];
  const seen = {};
  for (const [i, s] of services.entries()) {
    const c = eventClients[i];
    c.connect(`${host}:${__ENV.EVAL_PORT}`, { plaintext: true });
    clients.push(c);
    seen[s] = [];
    const stream = new Stream(c, `/${s}/EventStream`, null);
    stream.on('data', (d) => seen[s].push(d.type));
    stream.on('error', (e) => seen[s].push('error:' + String(e && e.message || e)));
    stream.write({});
  }
  await sleep(SECONDS * 1000 - D);
  say('events', 'final', { seen });
  clients.forEach((c) => c.close());
}
