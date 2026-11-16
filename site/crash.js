const STEPS = 8;
const CRASH_RATE = 0.3;
const MANUAL_CUT = 0.5;
const WARM_UP = 2;

export const seeded = (seed) => () => {
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

export const read = (disk) => {
  const lines = disk.split("\n");
  const tail = lines.pop();
  const found = new Map();
  const rows = [];
  let readable = true;
  for (const line of lines) {
    const parts = line.split("=");
    readable &&= parts.length === 2 && parts.every(Boolean);
    if (readable) found.set(parts[0], parts[1]);
    rows.push({ text: `${line}\n`, kind: readable ? "ok" : "bad" });
  }
  if (tail) rows.push({ text: tail, kind: "torn" });
  return { found, rows };
};

export const log = (fix) => {
  const state = { disk: "", acked: new Map(), next: 1, ops: [] };
  const record = () => {
    const n = state.next++;
    return [`k${n}`, `v${n}`];
  };
  return {
    state,
    put() {
      const [k, v] = record();
      state.disk += `${k}=${v}\n`;
      state.acked.set(k, v);
      state.ops.push(`put ${k}`);
    },
    crash(fraction) {
      const [k, v] = record();
      const line = `${k}=${v}\n`;
      const cut = 1 + Math.floor(fraction * (line.length - 1));
      state.disk += line.slice(0, cut);
      state.ops.push(`crash mid-put ${k}`);
      if (fix()) state.disk = read(state.disk).rows.filter((r) => r.kind === "ok").map((r) => r.text).join("");
    },
    lost() {
      const { found } = read(state.disk);
      return [...state.acked].filter(([k, v]) => found.get(k) !== v).map(([k]) => k);
    },
  };
};

export const play = (seed, fix) => {
  const rand = seeded(seed);
  const run = log(() => fix);
  for (let i = 0; i < STEPS; i++) {
    if (rand() < CRASH_RATE) run.crash(rand());
    else run.put();
  }
  return run;
};

const mount = (toy) => {
  const $ = (name) => toy.querySelector(`[data-${name}]`);
  const fix = () => $("fix").checked;
  const fresh = () => {
    const next = log(fix);
    for (let i = 0; i < WARM_UP; i++) next.put();
    return next;
  };
  let run = fresh();

  const span = (text, kind) => {
    const el = document.createElement("span");
    el.className = kind;
    el.textContent = text.replace("\n", "⏎");
    return el;
  };

  const render = () => {
    const { found, rows } = read(run.state.disk);
    const lost = run.lost();
    $("disk").replaceChildren(...rows.map((r) => span(r.text, r.kind)));
    $("acked").textContent = [...run.state.acked.keys()].join(" ") || "nothing yet";
    $("found").textContent = [...found.keys()].join(" ") || "nothing";
    $("lost").textContent = lost.length ? lost.join(" ") : "nothing";
    $("lost").dataset.state = lost.length ? "bad" : "ok";
    $("ops").textContent = run.state.ops.join(", ");
  };

  const actions = {
    put: () => run.put(),
    crash: () => run.crash(MANUAL_CUT),
    reset: () => { run = fresh(); },
    seed: () => { run = play(Number($("seed").value) || 0, fix()); },
  };

  toy.addEventListener("click", (e) => {
    const action = actions[e.target.dataset.action];
    if (!action) return;
    action();
    render();
  });
  render();
};

if (typeof document !== "undefined") document.querySelectorAll("[data-toy]").forEach(mount);
