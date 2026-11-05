// The oracle of the concurrency test (NFR-017). expected() is generations.go's Expected, in JavaScript; the fixture in
// testdata/fixture.json holds both to the same answers.
//
// An answer to a request sent at s and answered at e is correct iff it equals the expected answer of some generation g
// with lo <= g <= hi (windowOf), where generation g is written at T0 + g*P and applied within D.

export const PER_KIND = 10;
export const ODD_ONLY = 'a-odd-only';
const KINDS = ['gen', 'str', 'bool', 'flt', 'tgt', 'obj'];

export function setOf(key) {
  return key.split('-')[0];
}

// expected returns {found, variant, value} of key in generation g for a request with targetingKey.
export function expected(key, g, targetingKey) {
  const none = { found: false };
  if (key === ODD_ONLY) {
    return g % 2 === 0 ? none : { found: true, variant: 'on', value: true };
  }
  const parts = key.split('-');
  if (parts.length < 2 || (parts[0] !== 'a' && parts[0] !== 'b')) return none;
  let i = 0;
  if (parts.length === 3) {
    i = Number(parts[2]);
    if (!Number.isInteger(i) || i < 0 || i >= PER_KIND) return none;
  }
  switch (parts[1]) {
    case 'gen':
      return parts.length === 2 ? { found: true, variant: 'v', value: g } : none;
    case 'str':
      return { found: true, variant: 'v', value: `g${g}-${i}` };
    case 'bool':
      return (g + i) % 2 === 1 ? { found: true, variant: 'on', value: true } : { found: true, variant: 'off', value: false };
    case 'flt':
      return { found: true, variant: 'v', value: g * 1000 + i };
    case 'tgt': {
      const hit = String(targetingKey || '').endsWith(String(g % 10));
      return { found: true, variant: hit ? 'hit' : 'miss', value: hit ? 'hit' : 'miss' };
    }
    case 'obj':
      return { found: true, variant: 'v', value: { gen: g, i } };
  }
  return none;
}

// keys lists the keys generation g holds, optionally of one flag set.
export function keys(g, set) {
  const out = [];
  for (const s of ['a', 'b']) {
    if (set && s !== set) continue;
    out.push(`${s}-gen`);
    for (const kind of KINDS.slice(1)) {
      for (let i = 0; i < PER_KIND; i++) out.push(`${s}-${kind}-${i}`);
    }
  }
  if (g % 2 === 1 && (!set || set === 'a')) out.push(ODD_ONLY);
  return out;
}

export function deepEqual(a, b) {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null || typeof a !== 'object') return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  return ka.every((k) => deepEqual(a[k], b[k]));
}

export function same(answer, want) {
  if (answer.found !== want.found) return false;
  if (!want.found) return true;
  return answer.variant === want.variant && deepEqual(answer.value, want.value);
}

// windowOf returns the generations an answer to a request sent at s and answered at e may come from. cfg holds T0, P, D,
// EPS (ms) and N, the last generation written.
export function windowOf(s, e, cfg) {
  const hi = Math.min(cfg.N, Math.max(0, Math.floor((e - cfg.T0 + cfg.EPS) / cfg.P)));
  const lo = Math.min(hi, Math.max(0, Math.floor((s - cfg.T0 - cfg.D - cfg.EPS) / cfg.P)));
  return { lo, hi, wide: hi - lo + 1 > 2 };
}

function describe(answer) {
  return JSON.stringify(answer);
}

// checkAnswer classifies one answer: {ok} when it matches a generation in the window, otherwise {kind: 'stale'} when it
// matches only an older generation, or {kind: 'wrong'}.
export function checkAnswer(answer, key, targetingKey, s, e, cfg) {
  const w = windowOf(s, e, cfg);
  let older = false;
  for (let g = 0; g <= Math.min(cfg.N, w.hi + 3); g++) {
    if (!same(answer, expected(key, g, targetingKey))) continue;
    if (g >= w.lo && g <= w.hi) return { ok: true, wide: w.wide, window: w };
    if (g < w.lo) older = true;
  }
  return {
    ok: false,
    kind: older ? 'stale' : 'wrong',
    window: w,
    reason: `${key} (targetingKey ${targetingKey}) answered ${describe(answer)}, window ${w.lo}..${w.hi}, request ${s}..${e}`,
  };
}

// newState returns the per-VU state of the monotonic check.
export function newState() {
  return { gen: {} };
}

// checkMonotonic: a VU's later request never sees an older generation of a set's gen flag than an earlier one.
export function checkMonotonic(state, key, answer) {
  if (!key.endsWith('-gen') || !answer.found) return { ok: true };
  const set = setOf(key);
  const last = state.gen[set];
  if (last !== undefined && answer.value < last) {
    return { ok: false, kind: 'stale', reason: `${key} went backwards from ${last} to ${answer.value}` };
  }
  state.gen[set] = answer.value;
  return { ok: true };
}

// checkBulk: every flag of a bulk answer comes from one generation inside the window, and the answer holds exactly the
// flags of that generation (of one set when selectorSet is given). entries are {key, found, variant, value}; a flag the
// server left out is simply absent.
export function checkBulk(entries, targetingKey, s, e, cfg, selectorSet) {
  const w = windowOf(s, e, cfg);
  const got = new Map(entries.map((x) => [x.key, x]));
  const problems = [];
  for (let g = w.lo; g <= w.hi; g++) {
    const want = keys(g, selectorSet);
    const bad = [];
    if (got.size !== want.length) bad.push(`${got.size} flags, generation ${g} holds ${want.length}`);
    for (const k of want) {
      const entry = got.get(k);
      if (!entry) {
        bad.push(`${k} missing`);
      } else if (!same({ found: true, variant: entry.variant, value: entry.value }, expected(k, g, targetingKey))) {
        bad.push(`${k} differs`);
      }
      if (bad.length > 3) break;
    }
    if (bad.length === 0) return { ok: true, wide: w.wide, window: w };
    problems.push(`generation ${g}: ${bad.join(', ')}`);
  }
  // Is it one older generation entirely? Then it is stale rather than wrong.
  let kind = 'wrong';
  for (let g = 0; g < w.lo; g++) {
    if (keys(g, selectorSet).every((k) => got.has(k) && same({ found: true, variant: got.get(k).variant, value: got.get(k).value }, expected(k, g, targetingKey))) && got.size === keys(g, selectorSet).length) {
      kind = 'stale';
    }
  }
  return { ok: false, kind, window: w, reason: `bulk answer matches no single generation in ${w.lo}..${w.hi}: ${problems.join('; ')}` };
}

// payloadGeneration reads the generation a sync payload carries, from its gen flag of the first set present, and checks
// that every flag of the payload belongs to that generation. flags is the payload's "flags" object.
export function checkPayload(flags, selectorSet) {
  const names = Object.keys(flags);
  const set = selectorSet || 'a';
  const genFlag = flags[`${set}-gen`];
  if (!genFlag || !genFlag.variants || typeof genFlag.variants.v !== 'number') {
    return { ok: false, kind: 'wrong', reason: `payload has no ${set}-gen flag` };
  }
  const g = genFlag.variants.v;
  const want = keys(g, selectorSet);
  const problems = [];
  if (selectorSet) {
    for (const n of names) if (!n.startsWith(`${selectorSet}-`)) problems.push(`${n} is outside the selected flag set ${selectorSet}`);
  }
  if (names.length !== want.length) problems.push(`${names.length} flags, generation ${g} holds ${want.length}`);
  for (const k of want) {
    const f = flags[k];
    if (!f) {
      problems.push(`${k} missing`);
      continue;
    }
    const w = expected(k, g, '');
    if (k.includes('-tgt-')) {
      const ok = JSON.stringify(f.targeting).includes(`"${g % 10}"`) && f.variants && f.variants.hit === 'hit';
      if (!ok) problems.push(`${k} targeting is not generation ${g}'s`);
    } else if (f.defaultVariant !== w.variant || !deepEqual(f.variants[f.defaultVariant], w.value)) {
      problems.push(`${k} is not generation ${g}'s`);
    }
    if (problems.length > 3) break;
  }
  if (problems.length) return { ok: false, kind: 'wrong', reason: `sync payload of generation ${g}: ${problems.join(', ')}` };
  return { ok: true, gen: g };
}

// SyncChecker follows one sync stream: the first payload is inside the window, then each is the same generation or the
// next one (a repeat is accepted), and a selected stream holds only its set's keys. A jump of more than one is a lagging
// stream (stream_lag).
export class SyncChecker {
  constructor(cfg, selectorSet, name) {
    this.cfg = cfg;
    this.set = selectorSet || null;
    this.name = name;
    this.last = null;
    this.count = 0;
  }

  // payload returns {ok} or {ok:false, kind, reason}; s is when the stream was opened, e when this payload arrived.
  payload(flags, s, e) {
    const p = checkPayload(flags, this.set);
    if (!p.ok) return Object.assign({ stream: this.name }, p);
    const g = p.gen;
    this.count++;
    if (this.last === null) {
      const w = windowOf(s, e, this.cfg);
      this.last = g;
      if (g < w.lo || g > w.hi) {
        return { ok: false, kind: g < w.lo ? 'stale' : 'wrong', reason: `${this.name}: first payload is generation ${g}, window ${w.lo}..${w.hi}` };
      }
      return { ok: true, gen: g };
    }
    const prev = this.last;
    this.last = Math.max(prev, g);
    if (g === prev || g === prev + 1) return { ok: true, gen: g };
    if (g > prev + 1) return { ok: false, kind: 'lag', reason: `${this.name}: stream lagged, generation ${prev} was followed by ${g}` };
    return { ok: false, kind: 'wrong', reason: `${this.name}: generation went backwards from ${prev} to ${g}` };
  }

  // finish checks the stream reached the generation due at end - D.
  finish(now) {
    const w = windowOf(now, now, this.cfg);
    if (this.last === null) return { ok: false, kind: 'lag', reason: `${this.name}: no payload arrived` };
    if (this.last < w.lo) return { ok: false, kind: 'lag', reason: `${this.name}: ends at generation ${this.last}, generation ${w.lo} was due` };
    return { ok: true };
  }
}

// EventChecker counts configuration_change events of an EventStream: provider_ready first, keep_alive ignored, and at the
// end one change per generation written in (connect + D, end - D).
export class EventChecker {
  constructor(cfg, name, connectedAt) {
    this.cfg = cfg;
    this.name = name;
    this.connectedAt = connectedAt;
    this.types = [];
    this.changes = 0;
  }

  event(type) {
    this.types.push(type);
    if (this.types.length === 1 && type !== 'provider_ready') {
      return { ok: false, kind: 'wrong', reason: `${this.name}: first event is ${type}, not provider_ready` };
    }
    if (type === 'configuration_change') this.changes++;
    return { ok: true };
  }

  due(endAt) {
    const { T0, P, D } = this.cfg;
    let n = 0;
    for (let g = 1; g <= this.cfg.N; g++) {
      const w = T0 + g * P;
      if (w > this.connectedAt + D && w < endAt - D) n++;
    }
    return n;
  }

  finish(endAt) {
    if (this.types[0] !== 'provider_ready') return { ok: false, kind: 'wrong', reason: `${this.name}: no provider_ready` };
    const due = this.due(endAt);
    if (this.changes < due) {
      return { ok: false, kind: 'lag', reason: `${this.name}: ${this.changes} configuration_change events, ${due} generations were written` };
    }
    return { ok: true };
  }
}
