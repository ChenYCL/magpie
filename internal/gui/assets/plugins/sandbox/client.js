// The Sandbox page: the box an agent may run untrusted work in, and the plan
// it is (internal/sandbox). magpie holds no container engine's socket —
// the plan's broker is a program beside it (`magpie sandbox serve`), and
// everything here is a request to that program.
//
// The page is a plugin's (plugins/sandbox): its markup is the html below,
// drawn through the kernel's hooks, and its words are in i18n.js beside it.
// It is off by default — a broker is the user's to start — and says what to
// run when nothing answers at the plan's address.
magpie.plugin("sandbox", (ctx) => {
  let page = null;
  let state = null;   // GET /api/sandbox
  let err = "";
  let busy = false;
  let draft = null;   // the plan as being edited
  let trying = null;  // { command, out } of the Try
  let note = "";      // the last answer to Restore

  const { el, button, toggle, menu, row } = ctx.ui;

  const t = (s) => (window.t ? window.t(s) : s);
  function field(value, placeholder, onInput, type) {
    const i = el("input", "set-field");
    i.type = type || "text";
    i.value = value ?? "";
    if (placeholder) i.placeholder = placeholder;
    if (type === "number") { i.min = "1"; i.max = "8"; }
    i.oninput = () => onInput(type === "number" ? Number(i.value) : i.value);
    return i;
  }
  // a settings row with the window's control in it
  function setting(title, say, control) { return row(t(title), t(say), control); }

  async function load() {
    err = "";
    try {
      state = await api("sandbox");
      draft = JSON.parse(JSON.stringify(state.plan));
    } catch (e) {
      err = String(e.message || e);
    }
    render();
  }

  async function save(patch) {
    draft = { ...draft, ...patch };
    try {
      draft = await api("sandbox/save", draft);
      state.plan = draft;
      state = await api("sandbox"); // the broker's state with the new plan
      err = "";
    } catch (e) {
      err = String(e.message || e);
    }
    render();
  }

  async function tryCommand() {
    busy = true;
    err = "";
    render();
    try {
      const out = await api("sandbox/try", { command: trying.command, image: "" });
      trying.out = out.error ? { error: out.error } : out.result;
    } catch (e) {
      trying.out = { error: String(e.message || e) };
    }
    busy = false;
    render();
  }

  async function restore() {
    busy = true;
    render();
    try {
      const out = await api("sandbox/reset", {});
      note = out.error ? out.error : t("Removed {n} boxes").replace("{n}", out.removed);
      state = await api("sandbox");
    } catch (e) {
      note = String(e.message || e);
    }
    busy = false;
    render();
  }

  function render() {
    if (!page || !draft) return;
    const root = page.querySelector("#sbBody");
    if (!root) return;
    root.replaceChildren();
    if (err) {
      const bad = el("div", "row pref");
      bad.setAttribute("role", "alert");
      bad.append(el("div", "name", err));
      root.append(bad);
    }

    // what it is, and the one switch that matters
    const head = el("div", "row pref");
    const who = el("div", "who");
    who.append(el("div", "name", t("Run untrusted work in a box of its own")),
      el("div", "sub", t("A command runs in a fresh container: no host files, capabilities dropped, memory and CPU bounded, and no network unless this plan allows it.")));
    head.append(who, toggle(!!draft.on, t("Sandbox"), (on) => save({ on })));
    root.append(head);

    if (draft.on) {
      const broker = state?.up
        ? t("Broker answering at {url} · {running} of {max} boxes in use").replace("{url}", draft.broker).replace("{running}", state.running).replace("{max}", state.max)
        : t("No broker answers at {url} — run: magpie sandbox serve").replace("{url}", draft.broker);
      const b = el("div", "row pref");
      const bw = el("div", "who");
      bw.append(el("div", "name", t("Broker")), el("div", "sub", broker));
      b.append(bw);
      root.append(b);
      if (state?.broker) {
        const why = el("div", "row pref");
        const ww = el("div", "who");
        ww.append(el("div", "sub", state.broker));
        why.append(ww);
        root.append(why);
      }
    }

    // the plan
    root.append(setting("Image", "What a box is started from; it needs a shell.",
      field(draft.image, "debian:12-slim", (v) => { draft.image = v; }, "text")));
    root.append(setting("Network", "None keeps the box off every network; egress lets it out; host joins this computer's.",
      menu([
        { v: "none", name: t("None") },
        { v: "egress", name: t("Egress") },
        { v: "host", name: t("Host network") },
      ], draft.network, "", (v) => save({ network: v }))));
    const limits = draft.limits || {};
    root.append(setting("Memory (MB)", "A box's ceiling.",
      field(limits.memoryMB, "1024", (v) => { limits.memoryMB = v; }, "number")));
    root.append(setting("CPUs", "How many cores one box may use.",
      field(limits.cpus, "2", (v) => { limits.cpus = v; }, "number")));
    root.append(setting("Seconds", "How long one command may run before it is ended.",
      field(limits.seconds, "300", (v) => { limits.seconds = v; }, "number")));
    root.append(setting("Boxes at once", "2 to 4; more than this waits.",
      menu([2, 3, 4].map((n) => ({ v: n, name: String(n) })), limits.maxBoxes, "", (v) => save({ limits: { ...limits, maxBoxes: v } }))));

    const acts = el("div", "row pref");
    const aw = el("div", "who");
    aw.append(el("div", "name", t("Actions")));
    const a = el("div", "acts");
    a.append(button(t("Save plan"), () => save({}), "action"));
    a.append(button(t("Restore the boxes"), () => restore(), ""));
    aw.append(a);
    acts.append(aw);
    root.append(acts);
    if (note) {
      const n = el("div", "row pref");
      const nw = el("div", "who");
      nw.append(el("div", "sub", note));
      n.append(nw);
      root.append(n);
    }

    // the Try: one command, and what came back
    const tf = el("div", "row pref");
    const tw = el("div", "who");
    tw.append(el("div", "name", t("Try a command")));
    const line = el("div", "acts");
    const cmd = el("input", "set-field");
    cmd.type = "text";
    cmd.placeholder = "uname -a";
    cmd.value = trying ? trying.command : "";
    cmd.oninput = () => { trying = { command: cmd.value, out: trying?.out }; };
    line.append(cmd, button(busy ? t("Running…") : t("Run in a box"), () => tryCommand(), "action"));
    tw.append(line);
    tf.append(tw);
    root.append(tf);
    if (trying?.out) {
      const o = trying.out;
      const box = el("pre", "sb-out");
      box.textContent = o.error ? o.error : [
        t("exit {n}").replace("{n}", o.exit),
        o.stdout ? "--- stdout ---\n" + o.stdout.replace(/\n$/, "") : "",
        o.stderr ? "--- stderr ---\n" + o.stderr.replace(/\n$/, "") : "",
      ].filter(Boolean).join("\n");
      root.append(box);
    }
  }

  ctx.slots.add("page", {
    key: "sandbox",
    title: "Sandbox",
    html: `<div class="sb-page">
      <div class="sb-head"><h2 data-t>Sandbox</h2></div>
      <div id="sbBody" class="sb-body"></div>
    </div>`,
    mount(main) { page = main; },
    open: () => load(),
    relang: () => { if (page) render(); },
  });
});
