// The Model probe page: which model ids answer, at which providers, merged
// by id (internal/modelprobe). A row is one id — the same id served by four
// providers is one line — and opening it shows every provider of it and how
// each answered, which is what to paste when telling someone what they may
// use.
//
// Two ways to check: the standing round, a switch and an interval, and a
// run of one id or several now. Nothing runs while the plugin is off.
magpie.plugin("modelprobe", (ctx) => {
  let page = null;
  let state = null;    // GET /api/model-probe
  let err = "";
  let busy = false;    // a manual run is on
  let open = new Set();// ids whose providers are shown
  let filter = "";     // only ids containing this
  let onlyFail = false;
  let picked = new Set();// ids picked for a run of several
  let draft = null;    // the plan as being edited
  let note = "";

  const { el, button, toggle, menu, row } = ctx.ui;
  const t = (s) => (window.t ? window.t(s) : s);

  function field(value, placeholder, onInput, type) {
    const i = el("input", "set-field");
    i.type = type || "text";
    i.value = value ?? "";
    if (placeholder) i.placeholder = placeholder;
    i.oninput = () => onInput(i.value);
    return i;
  }
  function setting(title, say, control) { return row(t(title), t(say), control); }

  async function load(quiet) {
    err = "";
    try {
      state = await api("model-probe");
      if (!draft || !quiet) draft = { ...state.plan };
    } catch (e) {
      err = String(e.message || e);
    }
    render();
  }

  async function save(patch) {
    const next = { ...draft, ...patch };
    try {
      const out = await api("model-probe/save", next);
      draft = { ...out.plan };
      state.plan = out.plan;
      state.every = out.every;
      err = "";
    } catch (e) {
      err = String(e.message || e);
    }
    render();
  }

  // run: asks the core to check models now. One id and several are the same
  // call: what differs is what it names, and whether a row answers at once.
  async function run(models) {
    busy = true;
    note = "";
    err = "";
    render();
    try {
      const out = await api("model-probe/run", { models: models || [] });
      state.results = out.results;
      note = out.running ? t("A round was already running: this shows what it has") : "";
    } catch (e) {
      err = String(e.message || e);
    }
    busy = false;
    render();
  }

  // clock: how long ago a row answered, said in words a reader can act on
  function ago(ms) {
    if (!ms) return t("never");
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 90) return t("{n}s ago").replace("{n}", s);
    const m = Math.round(s / 60);
    if (m < 90) return t("{n}m ago").replace("{n}", m);
    return t("{n}h ago").replace("{n}", Math.round(m / 60));
  }

  function rows() {
    const all = state?.results?.rows || [];
    const f = filter.trim().toLowerCase();
    return all.filter((r) => {
      if (onlyFail && r.ok === r.n) return false;
      return !f || r.id.toLowerCase().includes(f);
    });
  }

  function bad(s) {
    return s.ok ? "" : (s.error || t("no answer"));
  }

  function render() {
    if (!page || !draft) return;
    const root = page.querySelector("#mpBody");
    if (!root) return;
    root.replaceChildren();
    if (err) {
      const b = el("div", "row pref");
      b.setAttribute("role", "alert");
      b.append(el("div", "name", err));
      root.append(b);
    }

    // the switch that matters, and what it is for
    const head = el("div", "row pref");
    const who = el("div", "who");
    who.append(el("div", "name", t("Check which model ids answer")),
      el("div", "sub", t("A standing round asks every exposed model the smallest request that tells a working one from a dead one. It is magpie's own request, counted in no usage total.")));
    head.append(who, toggle(!!draft.on, t("Model probe"), (on) => save({ on })));
    root.append(head);

    root.append(setting("Every", "How often the standing round runs. A round asks each provider's models one at a time, so a big install takes a while.",
      menu([
        { v: "15m", name: t("Every 15 minutes") },
        { v: "30m", name: t("Every 30 minutes") },
        { v: "1h", name: t("Every hour") },
        { v: "3h", name: t("Every 3 hours") },
        { v: "6h", name: t("Every 6 hours") },
      ], draft.every || "30m", "", (v) => save({ every: v }))));

    if (state?.results?.running) {
      const r = el("div", "row pref");
      const w = el("div", "who");
      w.append(el("div", "name", t("A round is running")),
        el("div", "sub", state.results.step || ""));
      r.append(w);
      root.append(r);
    }

    // a manual run: the search picks ids, and Run checks them
    const runRow = el("div", "row pref");
    const rw = el("div", "who");
    rw.append(el("div", "name", t("Check now")));
    const line = el("div", "acts");
    const q = el("input", "set-field mp-q");
    q.type = "search";
    q.placeholder = t("Filter ids…");
    q.value = filter;
    q.oninput = () => { filter = q.value; render(); };
    line.append(q);
    const all = button(t("Run every model"), () => run([]), "action");
    all.disabled = busy;
    line.append(all);
    if (picked.size) {
      line.append(button(busy ? t("Running…") : t("Run {n} picked").replace("{n}", picked.size),
        () => run([...picked].map((id) => firstProvider(id) ? firstProvider(id) + "/" + id : id)), "action"));
      line.append(button(t("Clear picks"), () => { picked.clear(); render(); }, ""));
    }
    const failToggle = el("label", "tick mp-fail");
    const cb = el("input");
    cb.type = "checkbox";
    cb.checked = onlyFail;
    cb.onchange = () => { onlyFail = cb.checked; render(); };
    failToggle.append(cb, el("span", "", t("Only ids with a provider down")));
    line.append(failToggle);
    rw.append(line);
    runRow.append(rw);
    root.append(runRow);
    if (note) {
      const n = el("div", "row pref");
      const nw = el("div", "who");
      nw.append(el("div", "sub", note));
      n.append(nw);
      root.append(n);
    }

    // what a round found: one line per id, its providers behind it
    const list = el("div", "mp-list");
    const shown = rows();
    if (!shown.length) {
      list.append(el("p", "hint", state?.results?.at
        ? t("No id matches the filter.")
        : t("No round has run yet. Turn the probe on, or run every model now.")));
    }
    for (const r of shown) list.append(rowOf(r));
    root.append(list);

    // the words a colleague needs: the ids that answered, one per line
    if (shown.length) {
      const tools = el("div", "row pref");
      const tw = el("div", "who");
      tw.append(el("div", "name", t("Hand these on")));
      const acts = el("div", "acts");
      acts.append(button(t("Copy the working ids"), () => copyIds(shown, true), ""));
      acts.append(button(t("Copy every id and its answer"), () => copyTable(shown), ""));
      tw.append(acts);
      tools.append(tw);
      root.append(tools);
    }
  }

  function firstProvider(id) {
    const list = state?.ids?.[id];
    return list && list.length ? list[0] : "";
  }

  function rowOf(r) {
    const box = el("div", "mp-row" + (r.ok === 0 ? " down" : r.ok < r.n ? " part" : ""));
    const lead = el("button", "mp-id", "");
    lead.type = "button";
    lead.onclick = () => {
      if (open.has(r.id)) open.delete(r.id); else open.add(r.id);
      render();
    };
    const caret = el("span", "mp-caret", open.has(r.id) ? "▾" : "▸");
    const pick = el("input");
    pick.type = "checkbox";
    pick.className = "mp-pick";
    pick.checked = picked.has(r.id);
    pick.onchange = () => {
      if (pick.checked) picked.add(r.id); else picked.delete(r.id);
      render();
    };
    lead.append(caret, el("code", "", r.id));
    const count = el("span", "mp-count" + (r.ok === 0 ? " none" : r.ok < r.n ? " some" : ""),
      t("{ok} of {n}").replace("{ok}", r.ok).replace("{n}", r.n));
    lead.append(count);
    box.append(pick, lead);
    const one = el("button", "mp-one", busy ? t("…") : t("Check"));
    one.type = "button";
    one.title = t("Check this id at every provider serving it");
    one.onclick = () => run([r.id]);
    box.append(one);
    if (!open.has(r.id) && r.ok < r.n) {
      // the failing ones said without opening: a line of what to look at
      const why = r.providers.filter((p) => !p.ok).map((p) => p.name || p.provider).join(", ");
      box.append(el("span", "mp-why", t("down at {who}").replace("{who}", why)));
    }
    if (open.has(r.id)) {
      const inner = el("div", "mp-providers");
      for (const p of r.providers) {
        const line = el("div", "mp-p" + (p.ok ? "" : " down"));
        line.append(el("span", "mp-pname", p.name || p.provider));
        line.append(el("span", "mp-psay", p.ok
          ? t("answering · {ms} ms").replace("{ms}", p.millis)
          : bad(p)));
        line.append(el("span", "mp-pago", ago(p.at)));
        inner.append(line);
      }
      box.append(inner);
    }
    return box;
  }

  async function copyIds(shown, okOnly) {
    const ids = shown.filter((r) => (okOnly ? r.ok > 0 : true)).map((r) => r.id);
    await copy(ids.join("\n"));
    note = t("Copied {n} ids").replace("{n}", ids.length);
    render();
  }

  async function copyTable(shown) {
    const lines = [["id", "ok", "providers"].join("\t")];
    for (const r of shown) {
      for (const p of r.providers) {
        lines.push([r.id, p.ok ? "ok" : "down", (p.name || p.provider) + (p.ok ? "" : ": " + bad(p))].join("\t"));
      }
    }
    await copy(lines.join("\n"));
    note = t("Copied {n} lines").replace("{n}", lines.length - 1);
    render();
  }

  function copy(text) {
    if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text);
    const a = el("textarea", "mp-clip");
    a.value = text;
    document.body.append(a);
    a.select();
    document.execCommand("copy");
    a.remove();
    return Promise.resolve();
  }

  ctx.slots.add("page", {
    key: "modelprobe",
    title: "Model probe",
    html: `<div class="mp-page">
      <div class="mp-head"><h2 data-t>Model probe</h2></div>
      <div id="mpBody" class="mp-body"></div>
    </div>`,
    mount(main) { page = main; },
    open: () => load(),
    relang: () => render(),
  });
});
