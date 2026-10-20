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

  const KINDS = { prompt: "Prompt", context: "Context", tools: "Tools" };
  const ACTIONS = {
    prompt: ["append", "prepend", "set", "drop"],
    context: ["allow", "deny", "compact"],
    tools: ["allow", "deny", "rename", "compact"],
  };

  // presets in the order the cards show them
  const presets = () => (data?.presets || []);
  const byId = (id) => presets().find((p) => p.id === id) || null;
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
    const isCur = p.id === (data.default || "");
    const c = el("button", "pr-card" + (p.id === sel ? " on" : ""));
    c.type = "button";
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
    c.onclick = () => { sel = p.id; trying = null; render(); };
    return c;
  }

  function startEdit(p) {
    draft = JSON.parse(JSON.stringify(p));
    err = "";
    render();
  }

  function patchRow(pa, i) {
    const r = el("div", "pr-row" + (pa.disabled ? " off" : ""));
    const kind = el("select");
    for (const k of ["prompt", "context", "tools"]) {
      const o = el("option", "", t(KINDS[k]));
      o.value = k;
      if (k === pa.kind) o.selected = true;
      kind.append(o);
    }
    kind.onchange = () => {
      pa.kind = kind.value;
      if (!ACTIONS[pa.kind].includes(pa.action)) pa.action = ACTIONS[pa.kind][0];
      render();
    };
    const act = el("select");
    for (const a of ACTIONS[pa.kind] || []) {
      const o = el("option", "", a);
      o.value = a;
      if (a === pa.action) o.selected = true;
      act.append(o);
    }
    act.onchange = () => { pa.action = act.value; render(); };
    const match = el("input");
    match.value = pa.match || "";
    match.placeholder = pa.kind === "tools" ? "Read|Write|mcp__*" : pa.kind === "context" ? "CLAUDE.md|AGENTS.md|*.md" : "skills";
    match.oninput = () => { pa.match = match.value; };
    let value = null;
    if (pa.kind === "prompt" || (pa.kind === "tools" && pa.action === "rename")) {
      value = el("textarea");
      value.value = pa.value || "";
      value.placeholder = pa.action === "rename" ? "new-name" : "text";
      value.oninput = () => { pa.value = value.value; };
    } else {
      value = el("span", "");
    }
    const on = el("input");
    on.type = "checkbox";
    on.checked = !pa.disabled;
    on.title = t("Off in this preset");
    on.onchange = () => { pa.disabled = !on.checked; render(); };
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
    const proto = el("select");
    for (const x of ["anthropic", "chat", "responses", "gemini"]) {
      const o = el("option", "", x);
      o.value = x;
      if (x === trying.proto) o.selected = true;
      proto.append(o);
    }
    proto.onchange = () => { trying.proto = proto.value; };
    const body = el("textarea");
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

  // one agent's row: its name, and the preset it gets (— for the default)
  function bindRow(name, id, current, onPick) {
    const r = el("div", "pr-bindrow");
    r.append(el("span", "pr-bindname", name));
    const sel = el("select");
    const none = el("option", "", "— " + t("Default for new agents"));
    none.value = "";
    sel.append(none);
    for (const p of presets()) {
      const o = el("option", "", p.name || p.id);
      o.value = p.id;
      if (current === p.id) o.selected = true;
      sel.append(o);
    }
    sel.onchange = () => onPick(sel.value);
    if (id) sel.setAttribute("aria-label", t("Preset for {agent}", { agent: name }));
    r.append(sel);
    return r;
  }

  function binding() {
    const s = el("section", "pr-sec");
    s.append(el("h3", "", t("Given to")));
    const grid = el("div", "pr-bind");
    grid.append(bindRow(t("Default for new agents"), "", data.default || "", async (v) => {
      await api("presets/bind", { default: true, preset: v }); await load();
    }));
    // the agents magpie knows, each with the preset it gets
    const known = data.known?.length ? data.known : (state?.agents || []).map((a) => ({ id: a.id, name: a.name }));
    for (const a of known) {
      grid.append(bindRow(a.name || a.id, a.id, (data.agents || {})[a.id] || "", async (v) => {
        await api("presets/bind", { agent: a.id, preset: v }); await load();
      }));
    }
    s.append(grid);
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
    head.append(el("div", "segs", t("Presets")));
    head.append(el("span", "grow"));
    const reload = el("button", "text sm", "↻");
    reload.type = "button";
    reload.title = t("Reload");
    reload.onclick = () => load();
    head.append(reload);
    root.append(head);
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

  ctx.slots.add("page", {
    key: "presets",
    title: "Agent presets",
    html: `<div class="presets-page" id="presetsRoot"></div>`,
    open: () => load(),
    relang: () => render(),
  });
});
