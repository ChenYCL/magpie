// The Agent presets page: the presets magpie ships and the user's, the
// patches of each, which agents get one, and what the last requests
// through a preset did.
//
// A page the presets plugin adds to the window (kernel.js), its words in
// i18n.js beside this file. The presets themselves are the core's
// (/api/presets), since the presets' store is magpie's and not a plugin's:
// this file draws it.
magpie.plugin("presets", (ctx) => {
  let data = null;        // GET /api/presets
  let sel = "";           // the preset the page is on
  let draft = null;       // the preset being edited (a copy)
  let trying = null;      // { proto, body, out, stats } of the Try panel
  let err = "";
  let tab = "presets";    // "presets" or "plugins"
  let plugins = null;     // GET /api/gateway-plugins
  let pdraft = null;      // a gateway plugin being edited
  let ptest = null;       // { body, out, error } of a plugin's Try
  let pknown = [];

  // ---- the window's own controls -------------------------------------------
  // Nothing here is a native <select>: magpie draws a pick in its own menu
  // (kernel.js's ui.menu), and a native one would be the one grey control in
  // a page of magpie's. Same for the switch and the field.

  const pick = (opts, value, onPick, head) => ctx.ui.menu(opts.map(([v, name]) => ({ v, name })), value, head || "", onPick);
  const switchOn = (on, label, onChange) => ctx.ui.toggle(!!on, label, onChange);
  function field(value, placeholder, onInput, cls) {
    const i = el("input", "set-field" + (cls ? " " + cls : ""));
    i.type = "text";
    i.value = value || "";
    if (placeholder) i.placeholder = placeholder;
    i.oninput = () => onInput(i.value);
    return i;
  }

  const KINDS = { prompt: "Prompt", context: "Context", tools: "Tools" };
  const ACTIONS = {
    prompt: ["append", "prepend", "set", "drop"],
    context: ["allow", "deny", "compact"],
    tools: ["allow", "deny", "rename", "compact"],
  };

  // presets in the order the cards show them
  const presets = () => (data?.presets || []);
  const byId = (id) => presets().find((p) => p.id === id) || null;
  // Standard is the preset magpie serves by default: what it did before
  // presets existed
  const Standard = () => "standard";
  const cur = () => byId(sel) || presets()[0] || null;

  async function load() {
    err = "";
    try {
      data = await api("presets");
    } catch (e) {
      err = e.message;
      data = { presets: [], agents: {}, known: [], recent: [] };
    }
    if (!sel || !byId(sel)) sel = data.default || presets()[0]?.id || "";
    render();
  }

  function count(kind, patches) {
    const ps = (patches || []).filter((p) => p.kind === kind);
    return { total: ps.length, off: ps.filter((p) => p.disabled).length };
  }

  // counters as dsh's cards show them: what the preset carries, and how
  // many of them are off in it
  function counters(patches) {
    const row = el("div", "cnt");
    for (const k of ["prompt", "context", "tools"]) {
      const c = count(k, patches);
      const s = el("span");
      s.append(el("b", "", t(KINDS[k])), document.createTextNode(" " + (c.total - c.off) + "/" + c.total));
      if (c.off) s.append(document.createTextNode(" · " + t("Skipped") + " " + c.off));
      row.append(s);
    }
    const all = (patches || []).length;
    const off = (patches || []).filter((p) => p.disabled).length;
    row.append(el("span", "", t("Total") + " " + (data.recent?.[0] ? all : all - off) + "/" + all));
    return row;
  }

  function card(p) {
    const isCur = p.id === (data.default || Standard());
    const c = el("div", "pr-card" + (p.id === sel ? " on" : "") + (p.id === isCur ? " cur" : ""));
    const nm = el("div", "nm");
    nm.append(el("span", "", p.name || p.id));
    if (p.version) nm.append(el("span", "ver", "v" + p.version));
    nm.append(el("span", "ver", t(p.builtin ? "Built-in" : "Custom")));
    if (isCur) nm.append(el("span", "kind", t("In use")));
    c.append(nm);
    c.append(el("div", "sub", p.sub || ""));
    c.append(counters(p.patches));
    const acts = el("div", "acts");
    const curB = el("button", "text sm", t(isCur ? "Current" : "Set as current"));
    curB.type = "button";
    curB.disabled = isCur;
    curB.onclick = async (e) => {
      e.stopPropagation();
      await api("presets/bind", { default: true, preset: p.id });
      await load();
    };
    const editB = el("button", "text sm", t("Edit"));
    editB.type = "button";
    editB.onclick = (e) => { e.stopPropagation(); startEdit(p); };
    acts.append(curB, editB);
    c.append(acts);
    c.onclick = (e) => {
      if (e.target.closest("button")) return; // its own buttons act
      sel = p.id;
      trying = null;
      render();
    };
    return c;
  }

  function startEdit(p) {
    draft = JSON.parse(JSON.stringify(p));
    err = "";
    render();
  }

  function patchRow(pa, i) {
    const r = el("div", "pr-row" + (pa.disabled ? " off" : ""));
    const kind = pick(["prompt", "context", "tools"].map((k) => [k, t(KINDS[k])]), pa.kind, (v) => {
      pa.kind = v;
      if (!ACTIONS[pa.kind].includes(pa.action)) pa.action = ACTIONS[pa.kind][0];
      render();
    }, t("Kind"));
    const act = pick((ACTIONS[pa.kind] || []).map((a) => [a, a]), pa.action, (v) => { pa.action = v; render(); }, t("Action"));
    const match = field(pa.match, pa.kind === "tools" ? "Read|Write|mcp__*" : pa.kind === "context" ? "CLAUDE.md|AGENTS.md|*.md" : "skills", (v) => { pa.match = v; });
    let value;
    if (pa.kind === "prompt" || (pa.kind === "tools" && pa.action === "rename")) {
      value = el("textarea", "pr-value");
      value.value = pa.value || "";
      value.placeholder = pa.action === "rename" ? t("new name") : t("text");
      value.oninput = () => { pa.value = value.value; };
    } else {
      value = el("span", "pr-none", "—");
    }
    const on = switchOn(!pa.disabled, t("Off in this preset"), (v) => { pa.disabled = !v; render(); });
    r.append(kind, act, match, value, on);
    return r;
  }

  function kindSection(kind) {
    const s = el("section", "pr-sec");
    const patches = (draft.patches || []).filter((p) => p.kind === kind);
    const c = count(kind, draft.patches);
    s.append(el("h3", "", t(KINDS[kind]) + " " + (c.total - c.off) + "/" + c.total));
    if (!patches.length) s.append(el("p", "pr-note", t("No patches: this preset leaves every request as it is.")));
    for (const pa of patches) s.append(patchRow(pa, draft.patches.indexOf(pa)));
    const add = el("button", "text sm", t("Add a patch"));
    add.type = "button";
    add.onclick = () => {
      draft.patches = draft.patches || [];
      const n = draft.patches.filter((p) => p.kind === kind).length + 1;
      draft.patches.push({ id: kind + "-" + n, kind, action: ACTIONS[kind][0], match: "", value: "" });
      render();
    };
    s.append(add);
    return s;
  }

  const SAMPLE = JSON.stringify({
    model: "claude-sonnet-5-5",
    max_tokens: 256,
    system: "You are a coding agent.\n\n<skills>\n- commit: make a commit\n</skills>",
    tools: [
      { name: "Read", description: "Read a file", input_schema: { type: "object", properties: { path: { type: "string", description: "the file" } } } },
      { name: "Bash", description: "Run a command", input_schema: { type: "object", properties: { command: { type: "string" } } } },
    ],
    messages: [{ role: "user", content: "list the files" }],
  }, null, 2);

  function trySection() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Try a request")));
    s.append(el("p", "pr-note", t("Paste a request body (Chat Completions, Anthropic Messages, Responses or Gemini) and see what this preset makes of it: the body the vendor gets and what each patch did.")));
    trying = trying || { proto: "anthropic", body: SAMPLE, out: "", stats: null };
    const proto = pick([["anthropic", "Anthropic Messages"], ["chat", "Chat Completions"], ["responses", "Responses"], ["gemini", "Gemini"]],
      trying.proto, (v) => { trying.proto = v; }, t("API"));
    const body = el("textarea", "pr-value");
    body.value = trying.body;
    body.rows = 10;
    body.oninput = () => { trying.body = body.value; };
    const run = el("button", "text sm", t("Try"));
    run.type = "button";
    run.onclick = async () => {
      err = "";
      try {
        const r = await api("presets/try", { preset: draft || cur(), proto: trying.proto, body: trying.body });
        trying.out = r.body;
        trying.stats = r.stats;
        render();
      } catch (e) {
        err = e.message;
        render();
      }
    };
    s.append(proto, body, run);
    if (trying.stats) {
      const st = trying.stats;
      s.append(el("p", "pr-note", tn(st)));
      s.append(el("pre", "pr-diff", trying.out));
    }
    return s;
  }

  function tn(st) {
    const one = (c) => c.applied + "/" + c.total + (c.skipped ? " · " + t("Skipped") + " " + c.skipped : "");
    return t("Prompt") + " " + one(st.prompt) + "  ·  " + t("Context") + " " + one(st.context) + "  ·  " + t("Tools") + " " + one(st.tools)
      + "   (" + st.bytesIn + " → " + st.bytesOut + " bytes)";
  }

  function editor() {
    const s = el("section", "pr-sec");
    const head = el("div", "pr-head");
    head.append(el("h3", "", draft.name || draft.id));
    head.append(el("span", "grow"));
    if (draft.builtin) {
      const reset = el("button", "text sm", t("Reset"));
      reset.type = "button";
      reset.onclick = async () => {
        await api("presets/reset", { id: draft.id });
        draft = null;
        await load();
      };
      head.append(reset);
    } else {
      const rm = el("button", "text sm", t("Remove"));
      rm.type = "button";
      rm.onclick = async () => {
        await api("presets/remove", { id: draft.id });
        draft = null;
        sel = "";
        await load();
      };
      head.append(rm);
    }
    const save = el("button", "text sm", t("Save"));
    save.type = "button";
    save.onclick = async () => {
      err = "";
      try {
        await api("presets/save", draft);
        draft = null;
        await load();
        status(t("Preset saved"), "ok");
      } catch (e) {
        err = e.message;
        render();
      }
    };
    const cancel = el("button", "text sm", t("Cancel"));
    cancel.type = "button";
    cancel.onclick = () => { draft = null; render(); };
    head.append(save, cancel);
    s.append(head);
    if (draft.builtin) s.append(el("p", "pr-note", t("This preset is one magpie ships. Saving keeps your version beside it; Reset puts the shipped one back.")));
    const about = el("textarea");
    about.value = draft.about || "";
    about.placeholder = t("What this preset does, and when to use it");
    about.rows = 3;
    about.oninput = () => { draft.about = about.value; };
    s.append(about);
    for (const k of ["prompt", "context", "tools"]) s.append(kindSection(k));
    s.append(trySection());
    s.append(el("p", "pr-note", t("A preset changes the request only. It never changes the model, the stream flag, or where the request is routed.")));
    if (err) s.append(el("p", "pr-err", err));
    return s;
  }

  // the preset pick: every preset, and the choice to follow the default
  function presetPick(current, onPick) {
    const opts = [["", "— " + t("Follow the default")], ...presets().map((p) => [p.id, p.name || p.id])];
    return pick(opts, current, onPick, t("Preset"));
  }

  function binding() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Every agent")));
    const top = el("div", "pr-row-line");
    top.append(el("span", "pr-lbl", t("Preset")));
    top.append(presetPick(data.default || Standard(), async (v) => {
      await api("presets/default", { preset: v });
      await load();
    }));
    s.append(top);
    s.append(el("p", "pr-note", t("One preset for every agent. An agent left out gets none: its requests go out as it sent them.")));

    // the agents left out: a chip each, and a menu that adds them — a
    // searchable, multi-pick grid of five a row, closed by Done
    const out = data.exclude || [];
    const byName = (a) => a?.name || a?.id || "";
    const head = el("div", "pr-head");
    head.append(el("h3", "", t("Leave an agent out")));
    head.append(el("span", "grow"));
    const add = el("button", "proto pick", t("Add agents…"));
    add.type = "button";
    add.onclick = (ev) => {
      ev.stopPropagation();
      const rest = (data.known || []).filter((a) => !out.includes(a.id));
      if (!rest.length) return;
      openProtoMenu(add, rest.map((a) => ({ v: a.id, name: byName(a) })), [], (picked) => {
        // Done: every pick excluded at once
        if (!picked.length) return;
        Promise.all(picked.map((id) => api("presets/exclude", { agent: id, excluded: true })))
          .then(() => load());
      }, t("Leave an agent out"), "pm-5col", "right", false, t("Find an agent"));
    };
    head.append(add);
    s.append(head);
    const chips = el("div", "chips");
    if (!out.length) chips.append(el("span", "pr-none", t("None: every agent gets the preset above.")));
    for (const id of out) {
      const a = (data.known || []).find((x) => x.id === id);
      const c = el("button", "chip", byName(a) + " ×");
      c.type = "button";
      c.title = t("Put it back");
      c.onclick = async () => {
        await api("presets/exclude", { agent: id, excluded: false });
        await load();
      };
      chips.append(c);
    }
    s.append(chips);
    s.append(el("p", "pr-note", t("An agent left out gets no preset: its requests go out exactly as it sent them.")));
    return s;
  }

  function recent() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Recent runs")));
    const rows = data.recent || [];
    if (!rows.length) {
      s.append(el("p", "pr-note", t("Nothing has gone through this preset yet.")));
      return s;
    }
    for (const r of rows.slice(0, 12)) {
      s.append(el("p", "pr-note", new Date(r.at).toLocaleTimeString() + " · " + (r.agent || "?") + " · " + (r.preset || "?") + " · " + r.model + " — " + tn(r.stats)));
    }
    return s;
  }

  function render() {
    const root = $("#presetsRoot");
    if (!root) return;
    root.replaceChildren();
    const head = el("div", "pr-head");
    const tabs = el("div", "segs");
    for (const [id, name] of [["presets", t("Preset")], ["plugins", t("Gateway plugins")]]) {
      const b = el("button", "opt" + (tab === id ? " on" : ""), name);
      b.type = "button";
      b.onclick = () => {
        tab = id;
        draft = null;
        render();
        if (id === "plugins" && !plugins) loadPlugins();
      };
      tabs.append(b);
    }
    head.append(tabs);
    head.append(el("span", "grow"));
    const reload = el("button", "text sm", "↻");
    reload.type = "button";
    reload.title = t("Reload");
    reload.onclick = () => load();
    head.append(reload);
    root.append(head);
    if (tab === "plugins") {
      root.append(pluginsPane());
      return;
    }
    root.append(el("p", "pr-note", t("Presets decide which prompt, context files and tools an agent's requests carry. A preset is a list of patches: nothing runs, a request is edited.")));
    const grid = el("div", "pr-grid");
    for (const p of presets()) grid.append(card(p));
    root.append(grid);
    const body = el("div", "pr-body");
    body.append(draft ? editor() : binding());
    if (!draft) body.append(recent());
    root.append(body);
    if (err) root.append(el("p", "pr-err", err));
  }

  // ---- gateway plugins: programs of the user's own on every request -------

  async function loadPlugins() {
    err = "";
    try {
      plugins = await api("gateway-plugins");
      pknown = plugins.known || [];
    } catch (e) {
      err = e.message;
      plugins = { plugins: [], example: null };
    }
    render();
  }

  function pluginCard(p) {
    const c = el("div", "pr-card" + (pdraft && pdraft.id === p.id ? " on" : ""));
    const nm = el("div", "nm");
    nm.append(el("span", "", p.name || p.id));
    nm.append(el("span", "ver", p.state === "ready" ? t("Ready") : p.state === "resting" ? t("Resting") + " " + (p.resting || "") : t("Off")));
    c.append(nm);
    c.append(el("div", "sub", p.url));
    const cnt = el("div", "cnt");
    cnt.append(el("span", "", t("Calls") + " " + p.calls));
    cnt.append(el("span", "", t("Answered") + " " + p.answered));
    if (p.failures) cnt.append(el("span", "", t("Failures") + " " + p.failures));
    if (p.mean) cnt.append(el("span", "", t("Mean") + " " + p.mean + " ms"));
    c.append(cnt);
    if (p.err) c.append(el("p", "pr-err", p.err));
    const acts = el("div", "acts");
    const ed = el("button", "text sm", t("Edit"));
    ed.type = "button";
    ed.onclick = () => { pdraft = JSON.parse(JSON.stringify(p)); ptest = null; render(); };
    acts.append(ed);
    const rm = el("button", "text sm", t("Remove"));
    rm.type = "button";
    rm.onclick = async () => {
      await api("gateway-plugins/remove", { id: p.id });
      pdraft = null;
      await loadPlugins();
    };
    acts.append(rm);
    c.append(acts);
    return c;
  }

  function pluginEditor() {
    const s = el("section", "pr-sec");
    const head = el("div", "pr-head");
    head.append(el("h3", "", pdraft.id ? pdraft.id : t("New gateway plugin")));
    head.append(el("span", "grow"));
    const save = el("button", "text sm", t("Save"));
    save.type = "button";
    save.onclick = async () => {
      err = "";
      try {
        await api("gateway-plugins/save", pdraft);
        pdraft = null;
        await loadPlugins();
        status(t("Saved"), "ok");
      } catch (e) {
        err = e.message;
        render();
      }
    };
    const cancel = el("button", "text sm", t("Cancel"));
    cancel.type = "button";
    cancel.onclick = () => { pdraft = null; render(); };
    head.append(save, cancel);
    s.append(head);

    const row = (label, node, why) => {
      const l = el("label", "", label);
      const d = el("div", "pr-field");
      d.append(node);
      if (why) d.append(el("p", "pr-note", why));
      s.append(l, d);
    };
    const idIn = field(pdraft.id, "redteam", (v) => { pdraft.id = v; });
    row(t("Id"), idIn, t("One word: how the plugin is named in the ledger and here."));

    const url = field(pdraft.url, "http://127.0.0.1:8790", (v) => { pdraft.url = v; });
    row(t("Address"), url, t("Where the plugin answers. magpie POSTs to /request and /usage there."));

    const sel = el("button", "proto pick", (pdraft.agents || []).length
      ? (pdraft.agents.length === 1 ? pdraft.agents[0] : t("{n} agents", { n: pdraft.agents.length }))
      : t("Every agent"));
    sel.type = "button";
    sel.onclick = (ev) => {
      ev.stopPropagation();
      openProtoMenu(sel, pknown.map((a) => ({ v: a.id, name: a.name || a.id })), [...(pdraft.agents || [])], (picked) => {
        pdraft.agents = picked;
        render();
      }, t("Agent"), "pm-5col", "right", false, t("Find an agent"));
    };
    const chips = el("div", "chips");
    for (const a of pdraft.agents || []) {
      const c = el("button", "chip", a + " ×");
      c.type = "button";
      c.onclick = () => { pdraft.agents = (pdraft.agents || []).filter((x) => x !== a); render(); };
      chips.append(c);
    }
    row(t("Agents"), (() => { const d = el("div", "stack"); d.append(sel, chips); return d; })(),
      t("Only these agents' requests go through it. Empty is every one."));

    const usage = switchOn(pdraft.usage, t("Send it the ledger"), (v) => { pdraft.usage = v; });
    row(t("Send it the ledger"), usage, t("Usage rows, for a plugin that analyses traffic rather than editing it."));

    const timeout = field(pdraft.timeout, "1500ms", (v) => { pdraft.timeout = v; });
    row(t("Timeout"), timeout, t("How long it has to answer a request. Past it, the request goes out as it came."));

    const off = switchOn(pdraft.disabled, t("Off"), (v) => { pdraft.disabled = v; });
    row(t("Off"), off, t("Saved without being asked."));

    s.append(pluginTry());
    s.append(el("p", "pr-note", t("A plugin that fails, times out or changes the model leaves the request exactly as it was: with every plugin off or failing, the gateway serves what it served before one was installed.")));
    if (err) s.append(el("p", "pr-err", err));
    return s;
  }

  function pluginTry() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Try the plugin")));
    const ex = plugins && plugins.requestExample;
    ptest = ptest || { body: JSON.stringify(ex || { model: "m", messages: [{ role: "user", content: "hi" }] }, null, 2), out: "", error: "" };
    const ta = el("textarea");
    ta.rows = 8;
    ta.value = ptest.body;
    ta.oninput = () => { ptest.body = ta.value; };
    const run = el("button", "text sm", t("Try"));
    run.type = "button";
    run.onclick = async () => {
      err = "";
      try {
        const r = await api("gateway-plugins/try", { plugin: pdraft, body: ptest.body, agent: (pdraft.agents || [])[0] || "", model: "claude-sonnet-5-5" });
        ptest.out = r.body || "";
        ptest.error = r.error || "";
        render();
      } catch (e) {
        ptest.error = e.message;
        render();
      }
    };
    s.append(ta, run);
    if (ptest.error) s.append(el("p", "pr-err", ptest.error));
    if (ptest.out) s.append(el("pre", "pr-diff", ptest.out));
    return s;
  }

  function pluginsPane() {
    const s = el("div", "pr-body");
    s.append(el("p", "pr-note", t("A gateway plugin is a program of your own that every request goes through, inside magpie: it is told the agent, the model and the provider, and may hand back the body the vendor gets. Any language, one HTTP endpoint.")));
    const grid = el("div", "pr-grid");
    for (const p of plugins?.plugins || []) grid.append(pluginCard(p));
    const add = el("button", "pr-card", "＋ " + t("New gateway plugin"));
    add.type = "button";
    add.onclick = () => { pdraft = { id: "", url: "", agents: [], usage: false, timeout: "" }; ptest = null; render(); };
    grid.append(add);
    s.append(grid);
    if (pdraft) s.append(pluginEditor());
    if (err) s.append(el("p", "pr-err", err));
    return s;
  }

  ctx.slots.add("page", {
    key: "presets",
    title: "Agent presets",
    html: `<div class="presets-page" id="presetsRoot"></div>`,
    open: () => (tab === "plugins" ? loadPlugins() : load()),
    relang: () => render(),
    title: "Agent presets",
  });
});
