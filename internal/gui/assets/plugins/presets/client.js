// The Agent presets page: the presets magpie ships and the user's, the
// patches of each, which agents get one, and what the last requests
// through a preset did — and, beside them, the gateway plugins.
//
// A page the presets plugin adds to the window (kernel.js). The presets
// themselves are the core's (/api/presets), since the presets' store is
// magpie's and not a plugin's: this file draws it, with the shapes the
// rest of magpie has — cards, text buttons, the window's own menu — and a
// centred dialog for anything that is edited or picked, the way shadcn's
// dialog reads: a header, a body that scrolls, a footer of actions.
//
// The words are in i18n.js, the core's: the page says nothing a plugin's
// own i18n.js would have to carry, so it stays one file.
magpie.plugin("presets", (ctx) => {
  let data = null;      // GET /api/presets
  let sel = "";         // the preset the page is on
  let draft = null;     // the preset being edited (in a dialog)
  let trying = null;    // { proto, body, out, stats } of a dialog's Try
  let err = "";
  let tab = "presets";  // "presets" or "plugins"
  let plugins = null;   // GET /api/gateway-plugins
  let pdraft = null;    // a gateway plugin being edited (in a dialog)
  let ptest = null;     // { body, out, error } of a plugin's Try
  let pknown = [];

  const Standard = () => "standard";

  // ---- the window's controls ------------------------------------------------
  // Nothing native: a pick is the window's menu (kernel.js's ui.menu), a
  // switch its toggle, and a field a field — so a page looks like the rest
  // of magpie and not like a form a browser drew.

  const pick = (opts, value, onPick, head) => ctx.ui.menu(opts.map(([v, name]) => ({ v, name })), value, head || "", onPick);
  const switchOn = (on, label, onChange) => ctx.ui.toggle(!!on, label, onChange);
  function field(value, placeholder, onInput) {
    const i = el("input", "set-field");
    i.type = "text";
    i.value = value || "";
    if (placeholder) i.placeholder = placeholder;
    i.oninput = () => onInput(i.value);
    return i;
  }

  // a button, the window's way: quiet, or the one that acts
  function btn(label, onClick, kind) {
    const b = el("button", kind === "primary" ? "btn-p" : "btn-s", label);
    b.type = "button";
    b.onclick = onClick;
    return b;
  }

  // A centred dialog, the way the rest of the window has one: a header, a
  // body that scrolls, a footer of actions. magpie's own .modal is used, so
  // it sits over the window, closes on Escape and on a click outside, and
  // gives the keyboard back where it was.
  function dialog({ title, note, body, actions, wide, picker }) {
    const ed = el("div", "editor framed pr-dlg" + (wide ? " wide" : "") + (picker ? " pr-pick" : ""));
    const head = el("div", "ehead");
    head.append(el("b", "", title));
    if (note) head.append(el("span", "note", note));
    const x = el("button", "icon pr-x", "✕");
    x.type = "button";
    x.setAttribute("aria-label", t("Close"));
    x.onclick = () => { pdraft = null; draft = null; closeModal(); };
    head.append(el("span", "grow"), x);
    const eb = el("div", "ebody");
    eb.append(...[].concat(body || []));
    const bar = el("div", "bar");
    bar.append(el("span", "grow"), ...(actions || []));
    ed.append(head, eb, bar);
    openModal(ed);
    $("#modal").classList.add("presets");
    return ed;
  }

  // ---- presets ---------------------------------------------------------------

  async function load() {
    err = "";
    try {
      data = await api("presets");
    } catch (e) {
      err = e.message;
      data = { presets: [], known: [], recent: [] };
    }
    if (!sel || !byId(sel)) sel = data.default || Standard();
    render();
  }

  const presets = () => (data?.presets || []);
  const byId = (id) => presets().find((p) => p.id === id) || null;
  const cur = () => byId(sel) || presets()[0] || null;
  const isCurrent = (p) => p.id === (data?.default || Standard());

  const KINDS = { prompt: "Prompt", context: "Context", tools: "Tools" };
  const ACTIONS = {
    prompt: ["append", "prepend", "set", "drop"],
    context: ["allow", "deny", "compact"],
    tools: ["allow", "deny", "rename", "compact"],
  };

  // one kind's patches, as "16/19 · 跳过 3"
  function tally(patches, kind) {
    const ps = (patches || []).filter((p) => p.kind === kind);
    const off = ps.filter((p) => p.disabled).length;
    return { on: ps.length - off, total: ps.length, off };
  }

  // A preset whose patches are all off changes nothing: it is magpie as it
  // was, and says so rather than showing 0/1 three times.
  function counters(patches) {
    const all = patches || [];
    if (!all.length || all.every((p) => p.disabled)) {
      return el("div", "pr-cnt pr-cnt-none", t("Changes nothing: magpie's own behaviour"));
    }
    const row = el("div", "pr-cnt");
    for (const k of ["prompt", "context", "tools"]) {
      const c = tally(all, k);
      if (!c.total) continue;
      const s = el("span");
      s.append(el("i", "", t(KINDS[k])), document.createTextNode(` ${c.on}/${c.total}`));
      // how many of them the preset leaves off, only where there are any
      if (c.off) s.append(el("em", "", ` · ${t("Skipped")} ${c.off}`));
      row.append(s);
    }
    return row;
  }

  function card(p) {
    const c = el("div", "pr-card" + (p.id === sel ? " sel" : "") + (isCurrent(p) ? " cur" : ""));
    const top = el("div", "pr-card-top");
    top.append(el("span", "pr-card-name", p.name || p.id));
    if (isCurrent(p)) top.append(el("span", "pr-badge", t("In use")));
    c.append(top);
    c.append(el("p", "pr-card-sub", p.sub || ""));
    c.append(counters(p.patches));
    const acts = el("div", "pr-card-acts");
    acts.append(
      btn(t(isCurrent(p) ? "Current" : "Set as current"), async (e) => {
        e.stopPropagation();
        await api("presets/default", { preset: p.id });
        await load();
      }, isCurrent(p) ? "" : ""),
      btn(t("Edit"), (e) => { e.stopPropagation(); editPreset(p); }),
    );
    acts.lastChild.classList.add("em");
    c.append(acts);
    c.onclick = (e) => {
      if (e.target.closest("button")) return;
      sel = p.id;
      render();
    };
    return c;
  }

  // the editor: one dialog, its patches by kind, the about text, and a Try
  function editPreset(p) {
    draft = JSON.parse(JSON.stringify(p));
    trying = null;
    err = "";
    openPresetDialog();
  }

  function openPresetDialog() {
    const d = draft;
    const body = [];
    const about = el("textarea", "pr-value");
    about.rows = 3;
    about.value = d.about || "";
    about.placeholder = t("What this preset does, and when to use it");
    about.oninput = () => { d.about = about.value; };
    body.push(section(t("About"), [about]));

    for (const kind of ["prompt", "context", "tools"]) {
      const c = tally(d.patches, kind);
      const rows = el("div", "pr-rows");
      for (const pa of (d.patches || []).filter((x) => x.kind === kind)) rows.append(patchRow(pa, d));
      const add = btn(t("Add a patch"), () => {
        d.patches = d.patches || [];
        const n = d.patches.filter((x) => x.kind === kind).length + 1;
        d.patches.push({ id: `${kind}-${n}`, kind, action: ACTIONS[kind][0], match: "", value: "" });
        openPresetDialog();
      });
      add.classList.add("pr-add");
      rows.append(add);
      body.push(section(`${t(KINDS[kind])} ${c.on}/${c.total}`, [rows]));
    }
    body.push(pluginTrySection());

    const actions = [];
    if (d.builtin) {
      actions.push(btn(t("Reset"), async () => { await api("presets/reset", { id: d.id }); draft = null; closeModal(); await load(); }));
    } else {
      actions.push(btn(t("Remove"), async () => { await api("presets/remove", { id: d.id }); draft = null; closeModal(); await load(); }));
    }
    actions.push(btn(t("Cancel"), () => { draft = null; closeModal(); }));
    actions.push(btn(t("Save"), async () => {
      err = "";
      // a patch that names nothing (match) and adds nothing (value) says
      // nothing: drop it, rather than refuse the whole save over it.
      // append/prepend carry their text in value, not match.
      draft.patches = (draft.patches || []).filter((pa) =>
        pa.disabled || pa.match.trim() !== "" || (["append","prepend"].includes(pa.action) && (pa.value||"").trim() !== ""));
      try {
        await api("presets/save", draft);
        draft = null;
        closeModal();
        await load();
        status(t("Saved"), "ok");
      } catch (e) {
        err = e.message;
        openPresetDialog();
      }
    }, "primary"));

    const notes = [];
    if (d.builtin) notes.push(el("p", "pr-note", t("This preset is one magpie ships. Saving keeps your version beside it; Reset puts the shipped one back.")));
    notes.push(el("p", "pr-note", t("A preset changes the request only. It never changes the model, the stream flag, or where the request is routed.")));
    if (err) notes.push(el("p", "pr-err", err));

    dialog({ title: d.name || d.id, note: t("Preset"), body: [...body, ...notes], actions, wide: true });
  }

  function section(title, kids) {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", title));
    s.append(...kids);
    return s;
  }

  function patchRow(pa, d) {
    const r = el("div", "pr-prow" + (pa.disabled ? " off" : ""));
    r.append(pick(["prompt", "context", "tools"].map((k) => [k, t(KINDS[k])]), pa.kind, (v) => {
      pa.kind = v;
      if (!ACTIONS[pa.kind].includes(pa.action)) pa.action = ACTIONS[pa.kind][0];
      openPresetDialog();
    }, t("Kind")));
    r.append(pick((ACTIONS[pa.kind] || []).map((a) => [a, a]), pa.action, (v) => { pa.action = v; openPresetDialog(); }, t("Action")));
    r.append(field(pa.match, pa.kind === "tools" ? "Read|Write|mcp__*" : pa.kind === "context" ? "CLAUDE.md|AGENTS.md" : "skills", (v) => { pa.match = v; }));
    if (pa.kind === "prompt" || (pa.kind === "tools" && pa.action === "rename")) {
      const t2 = el("textarea", "pr-value");
      t2.value = pa.value || "";
      t2.placeholder = pa.action === "rename" ? t("new name") : t("text");
      t2.oninput = () => { pa.value = t2.value; };
      r.append(t2);
    } else {
      r.append(el("span", "pr-dash", "—"));
    }
    r.append(switchOn(!pa.disabled, t("Off in this preset"), (v) => { pa.disabled = !v; openPresetDialog(); }));
    const rm = el("button", "pr-rowdel", "✕");
    rm.type = "button";
    rm.title = t("Remove this patch");
    rm.setAttribute("aria-label", t("Remove this patch"));
    rm.onclick = () => {
      d.patches = (d.patches || []).filter((x) => x !== pa);
      openPresetDialog();
    };
    r.append(rm);
    return r;
  }

  // Try, inside the dialog: a sample body, the reply, and what each patch did
  function pluginTrySection() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Try a request")));
    s.append(el("p", "pr-note", t("Paste a request body (Chat Completions, Anthropic Messages, Responses or Gemini) and see what this preset makes of it.")));
    trying = trying || { proto: "anthropic", body: SAMPLE, out: "", stats: null };
    const row = el("div", "pr-tryrow");
    row.append(pick([["anthropic", "Anthropic Messages"], ["chat", "Chat Completions"], ["responses", "Responses"], ["gemini", "Gemini"]], trying.proto, (v) => { trying.proto = v; openPresetDialog(); }, t("API")));
    row.append(btn(t("Try"), async () => {
      try {
        const r = await api("presets/try", { preset: draft, proto: trying.proto, body: trying.body });
        trying.out = r.body;
        trying.stats = r.stats;
      } catch (e) {
        err = e.message;
      }
      openPresetDialog();
    }));
    s.append(row);
    const ta = el("textarea", "pr-value pr-json");
    ta.rows = 7;
    ta.value = trying.body;
    ta.oninput = () => { trying.body = ta.value; };
    s.append(ta);
    if (trying.stats) s.append(el("p", "pr-note", tn(trying.stats)), el("pre", "pr-diff", trying.out));
    return s;
  }

  function tn(st) {
    const one = (c) => `${c.applied}/${c.total}` + (c.skipped ? ` · ${t("Skipped")} ${c.skipped}` : "");
    return `${t("Prompt")} ${one(st.prompt)}  ·  ${t("Context")} ${one(st.context)}  ·  ${t("Tools")} ${one(st.tools)}   (${st.bytesIn} → ${st.bytesOut})`;
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

  // every agent gets the preset picked here; the ones left out get none
  function everyAgent() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Every agent")));
    const line = el("div", "pr-line");
    line.append(el("span", "pr-lbl", t("Preset")));
    line.append(pick(presets().map((p) => [p.id, p.name || p.id]), data.default || Standard(), async (v) => {
      await api("presets/default", { preset: v });
      await load();
    }, t("Preset")));
    s.append(line);
    s.append(el("p", "pr-note", t("One preset for every agent. An agent left out gets none: its requests go out as it sent them.")));

    const out = data.exclude || [];
    const head = el("div", "pr-line");
    head.append(el("span", "pr-lbl", t("Leave an agent out")));
    head.append(btn(t("Add agents…"), () => chooseAgents(out), ""));
    s.append(head);
    const chips = el("div", "chips");
    if (!out.length) chips.append(el("span", "pr-dash", t("None: every agent gets the preset above.")));
    const byID = (id) => (data.known || []).find((a) => a.id === id);
    for (const id of out) {
      const c = el("button", "chip", (byID(id)?.name || id) + "  ✕");
      c.type = "button";
      c.title = t("Put it back");
      c.onclick = async () => {
        await api("presets/exclude", { agent: id, excluded: false });
        await load();
      };
      chips.append(c);
    }
    s.append(chips);
    return s;
  }

  // the agents left out, picked in a dialog: a search box over a grid of
  // five a row, every pick a toggle, and Done applies them at once
  function chooseAgents(out) {
    const all = (data.known || []);
    let picked = new Set(out);
    let q = "";
    const body = [];
    const search = el("input", "set-field pr-search");
    search.type = "search";
    search.placeholder = t("Find an agent");
    search.setAttribute("aria-label", t("Find an agent"));
    const grid = el("div", "pr-agents");
    const paint = () => {
      grid.replaceChildren();
      const words = q.trim().toLocaleLowerCase();
      const list = all.filter((a) => !words || (a.name || a.id).toLocaleLowerCase().includes(words) || a.id.includes(words));
      if (!list.length) grid.append(el("span", "pr-dash", t("No agent by that name")));
      for (const a of list) {
        const on = picked.has(a.id);
        const b = el("button", "pr-agent" + (on ? " on" : ""), a.name || a.id);
        b.type = "button";
        b.setAttribute("role", "checkbox");
        b.setAttribute("aria-checked", String(on));
        b.title = a.id;
        b.onclick = () => {
          on ? picked.delete(a.id) : picked.add(a.id);
          b.classList.toggle("on", picked.has(a.id));
          b.setAttribute("aria-checked", String(picked.has(a.id)));
          done.textContent = picked.size ? t("Leave out {n}", { n: picked.size }) : t("Done");
        };
        grid.append(b);
      }
    };
    search.oninput = () => { q = search.value; paint(); };
    paint();
    body.push(search, grid);
    body.push(el("p", "pr-note", t("An agent left out gets no preset: its requests go out exactly as it sent them.")));
    const done = btn(t("Done"), async () => {
      const now = new Set(out);
      const adds = [...picked].filter((id) => !now.has(id));
      const dels = [...now].filter((id) => !picked.has(id));
      closeModal();
      await Promise.all([
        ...adds.map((id) => api("presets/exclude", { agent: id, excluded: true })),
        ...dels.map((id) => api("presets/exclude", { agent: id, excluded: false })),
      ]);
      await load();
    }, "primary");
    dialog({ title: t("Leave an agent out"), note: `${all.length}`, body, actions: [btn(t("Cancel"), () => closeModal()), done], picker: true });
    search.focus({ preventScroll: true });
  }

  function recent() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Recent runs")));
    const rows = data.recent || [];
    if (!rows.length) {
      s.append(el("p", "pr-dash", t("Nothing has gone through this preset yet.")));
      return s;
    }
    const list = el("div", "pr-runs");
    for (const r of rows.slice(0, 12)) {
      const line = el("div", "pr-run");
      line.append(el("span", "pr-run-t", new Date(r.at).toLocaleTimeString()));
      line.append(el("span", "pr-run-a", r.agent || "?"));
      line.append(el("span", "pr-run-m", r.model || ""));
      const c = el("span", "pr-run-c", tn(r.stats));
      line.append(c);
      list.append(line);
    }
    s.append(list);
    return s;
  }

  // ---- gateway plugins ---------------------------------------------------------

  async function loadPlugins() {
    err = "";
    try {
      plugins = await api("gateway-plugins");
      pknown = plugins.known || [];
    } catch (e) {
      err = e.message;
      plugins = { plugins: [], requestExample: null };
    }
    render();
  }

  function pluginCard(p) {
    const c = el("div", "pr-card");
    const top = el("div", "pr-card-top");
    top.append(el("span", "pr-card-name", p.name || p.id));
    const state = p.state === "ready" ? t("Ready") : p.state === "resting" ? t("Resting") : t("Off");
    top.append(el("span", "pr-badge" + (p.state === "ready" ? " ok" : p.state === "resting" ? " warn" : ""), state));
    c.append(top);
    c.append(el("p", "pr-card-sub", p.url));
    const cnt = el("div", "pr-cnt");
    cnt.append(el("span", "", `${t("Calls")} ${p.calls}`), el("span", "", `${t("Answered")} ${p.answered}`));
    if (p.failures) cnt.append(el("span", "", `${t("Failures")} ${p.failures}`));
    if (p.mean) cnt.append(el("span", "", `${t("Mean")} ${p.mean} ms`));
    c.append(cnt);
    if (p.err) c.append(el("p", "pr-err", p.err));
    const acts = el("div", "pr-card-acts");
    acts.append(
      btn(t("Edit"), () => editPlugin(p)),
      btn(t("Remove"), async () => {
        await api("gateway-plugins/remove", { id: p.id });
        await loadPlugins();
      }),
    );
    acts.lastChild.classList.add("em");
    c.append(acts);
    return c;
  }

  function editPlugin(p) {
    pdraft = p ? JSON.parse(JSON.stringify(p)) : { id: "", url: "", agents: [], usage: false, timeout: "" };
    ptest = null;
    err = "";
    openPluginDialog();
  }

  function openPluginDialog() {
    const d = pdraft;
    const body = [];
    const rows = el("div", "pr-fields");
    const row = (label, node, why) => {
      const l = el("div", "pr-field");
      l.append(el("label", "pr-lbl", label), node);
      if (why) l.append(el("p", "pr-note", why));
      rows.append(l);
    };
    row(t("Id"), field(d.id, "redteam", (v) => { d.id = v; }), t("One word: how the plugin is named in the ledger and here."));
    row(t("Address"), field(d.url, "http://127.0.0.1:8790", (v) => { d.url = v; }), t("Where the plugin answers. magpie POSTs to /request and /usage there."));

    // its agents: a pick that opens the same grid, and a chip each
    const agentsBtn = el("button", "proto pick", (d.agents || []).length ? (d.agents.length === 1 ? d.agents[0] : t("{n} agents", { n: d.agents.length })) : t("Every agent"));
    agentsBtn.type = "button";
    const chips = el("div", "chips");
    const paintChips = () => {
      chips.replaceChildren();
      for (const a of d.agents || []) {
        const c = el("button", "chip", a + "  ✕");
        c.type = "button";
        c.onclick = () => { d.agents = (d.agents || []).filter((x) => x !== a); openPluginDialog(); };
        chips.append(c);
      }
    };
    paintChips();
    agentsBtn.onclick = () => {
      openProtoMenu(agentsBtn, pknown.map((a) => ({ v: a.id, name: a.name || a.id })), [...(d.agents || [])], (picked) => {
        d.agents = picked;
        openPluginDialog();
      }, t("Agent"), "pm-5col", "right", false, t("Find an agent"));
    };
    row(t("Agents"), (() => { const s = el("div", "stack"); s.append(agentsBtn, chips); return s; })(), t("Only these agents' requests go through it. Empty is every one."));
    row(t("Send it the ledger"), switchOn(d.usage, t("Send it the ledger"), (v) => { d.usage = v; }), t("Usage rows, for a plugin that analyses traffic rather than editing it."));
    row(t("Timeout"), field(d.timeout, "1500ms", (v) => { d.timeout = v; }), t("How long it has to answer a request. Past it, the request goes out as it came."));
    row(t("Off"), switchOn(d.disabled, t("Off"), (v) => { d.disabled = v; }), t("Saved without being asked."));
    body.push(rows);
    body.push(pluginTry());

    const actions = [btn(t("Cancel"), () => { pdraft = null; closeModal(); }), btn(t("Save"), async () => {
      err = "";
      try {
        await api("gateway-plugins/save", d);
        pdraft = null;
        closeModal();
        await loadPlugins();
        status(t("Saved"), "ok");
      } catch (e) {
        err = e.message;
        openPluginDialog();
      }
    }, "primary")];
    if (err) body.push(el("p", "pr-err", err));
    body.push(el("p", "pr-note", t("A plugin that fails, times out or changes the model leaves the request exactly as it was.")));
    dialog({ title: d.id || t("New gateway plugin"), note: t("Gateway plugin"), body, actions, wide: true });
  }

  function pluginTry() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Try the plugin")));
    ptest = ptest || { body: JSON.stringify(plugins?.requestExample?.body || { model: "m", messages: [] }, null, 2), out: "", error: "" };
    const row = el("div", "pr-tryrow");
    row.append(btn(t("Try"), async () => {
      try {
        const r = await api("gateway-plugins/try", { plugin: pdraft, body: ptest.body, agent: (pdraft.agents || [])[0] || "", model: "claude-sonnet-5-5" });
        ptest.out = r.body || "";
        ptest.error = r.error || "";
      } catch (e) {
        ptest.error = e.message;
      }
      openPluginDialog();
    }));
    s.append(row);
    const ta = el("textarea", "pr-value pr-json");
    ta.rows = 8;
    ta.value = ptest.body;
    ta.oninput = () => { ptest.body = ta.value; };
    s.append(ta);
    if (ptest.error) s.append(el("p", "pr-err", ptest.error));
    if (ptest.out) s.append(el("pre", "pr-diff", ptest.out));
    return s;
  }

  function pluginsPage() {
    const wrap = el("div", "pr-body");
    wrap.append(el("p", "pr-note", t("A gateway plugin is a program of your own that every request goes through, inside magpie: it is told the agent, the model and the provider, and may hand back the body the vendor gets. Any language, one HTTP endpoint.")));
    const grid = el("div", "pr-grid");
    for (const p of plugins?.plugins || []) grid.append(pluginCard(p));
    const add = el("button", "pr-card pr-new", "＋ " + t("New gateway plugin"));
    add.type = "button";
    add.onclick = () => editPlugin(null);
    grid.append(add);
    wrap.append(grid);
    if (err) wrap.append(el("p", "pr-err", err));
    return wrap;
  }

  // ---- the page ---------------------------------------------------------------

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
        render();
        if (id === "plugins" && !plugins) loadPlugins();
      };
      tabs.append(b);
    }
    head.append(tabs);
    head.append(el("span", "grow"));
    head.append(btn("↻", () => (tab === "plugins" ? loadPlugins() : load())));
    root.append(head);

    if (tab === "plugins") {
      root.append(pluginsPage());
      return;
    }
    root.append(el("p", "pr-note", t("Presets decide which prompt, context files and tools an agent's requests carry. A preset is a list of patches: nothing runs, a request is edited.")));
    const grid = el("div", "pr-grid");
    for (const p of presets()) grid.append(card(p));
    root.append(grid);
    const body = el("div", "pr-body");
    body.append(everyAgent(), recent());
    root.append(body);
    if (err) root.append(el("p", "pr-err", err));
  }

  ctx.slots.add("page", {
    key: "presets",
    title: "Agent presets",
    html: `<div class="presets-page" id="presetsRoot"></div>`,
    open: () => (tab === "plugins" ? loadPlugins() : load()),
    relang: () => render(),
  });
});
