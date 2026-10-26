// tau annotate overlay: comment on a text selection or a whole section, sent to
// a Pi session. Injected by server.py; config arrives in window.__TAU_ANNOTATE.
(() => {
  const cfg = window.__TAU_ANNOTATE;
  if (!cfg || document.getElementById("tau-annotate")) return;

  const root = document.querySelector("article.markdown-body, main, article") || document.body;
  const HEADINGS = "h1,h2,h3,h4,h5,h6";
  const level = (h) => Number(h.tagName[1]);
  const headings = () => [...root.querySelectorAll(HEADINGS)];
  const headingText = (h) => h.textContent.replace(/\s+/g, " ").trim();

  // Heading chain ("Findings › Latency") for the section a node sits in.
  function sectionOf(node) {
    const el = node.nodeType === 1 ? node : node.parentElement;
    let target = null;
    for (const h of headings()) {
      if (h === el || h.contains(el) || h.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING) target = h;
      else break;
    }
    if (!target) return { heading: null, path: "", id: "" };
    const stack = [];
    for (const h of headings()) {
      while (stack.length && level(stack[stack.length - 1]) >= level(h)) stack.pop();
      stack.push(h);
      if (h === target) break;
    }
    return { heading: target, path: stack.map(headingText).join(" › "), id: target.id || "" };
  }

  // Range covering a heading and everything up to the next heading of the same or higher level.
  function sectionRange(h) {
    const range = document.createRange();
    range.setStartBefore(h);
    let end = h;
    for (let n = h.nextElementSibling; n; n = n.nextElementSibling) {
      if (n.matches(HEADINGS) && level(n) <= level(h)) break;
      end = n;
    }
    range.setEndAfter(end);
    return range;
  }

  const style = document.createElement("style");
  style.textContent = "::highlight(tau-annotate){background:rgba(255,196,0,.35)}";
  document.head.appendChild(style);
  function highlight(range) {
    if (window.CSS && CSS.highlights && window.Highlight) {
      CSS.highlights.set("tau-annotate", new Highlight(range));
    }
  }
  function unhighlight() {
    if (window.CSS && CSS.highlights) CSS.highlights.delete("tau-annotate");
  }

  const host = document.createElement("div");
  host.id = "tau-annotate";
  const shadow = host.attachShadow({ mode: "open" });
  shadow.innerHTML = `
    <style>
      :host { all: initial; }
      * { box-sizing: border-box; font: 13px/1.4 -apple-system, system-ui, sans-serif; }
      .ui { --bg:#fff; --fg:#1f2328; --mut:#656d76; --bd:#d0d7de; --acc:#0969da; --ok:#1a7f37; --err:#cf222e;
            position: fixed; inset: 0; pointer-events: none; z-index: 2147483647; color: var(--fg); }
      @media (prefers-color-scheme: dark) {
        .ui { --bg:#161b22; --fg:#e6edf3; --mut:#8b949e; --bd:#30363d; --acc:#58a6ff; --ok:#3fb950; --err:#f85149; }
      }
      .ui > * { pointer-events: auto; }
      button { cursor: pointer; border: 1px solid var(--bd); background: var(--bg); color: var(--fg);
               border-radius: 6px; padding: 4px 10px; }
      button.primary { background: var(--acc); border-color: var(--acc); color: #fff; }
      button:disabled { opacity: .5; cursor: default; }
      .float { position: fixed; box-shadow: 0 2px 10px rgba(0,0,0,.25); }
      .pill { right: 14px; bottom: 14px; border-radius: 999px; padding: 5px 12px; color: var(--mut); }
      .panel { width: 380px; max-width: calc(100vw - 16px); background: var(--bg); border: 1px solid var(--bd);
               border-radius: 10px; padding: 12px; display: flex; flex-direction: column; gap: 8px; }
      .ctx { color: var(--mut); font-size: 12px; }
      .ctx b { color: var(--fg); font-weight: 600; }
      .quote { border-left: 3px solid var(--bd); padding: 2px 8px; color: var(--mut); max-height: 4.2em;
               overflow: hidden; white-space: pre-wrap; }
      textarea { width: 100%; min-height: 84px; resize: vertical; padding: 8px; border-radius: 6px;
                 border: 1px solid var(--bd); background: var(--bg); color: var(--fg); }
      textarea:focus { outline: 2px solid var(--acc); outline-offset: -1px; }
      .row { display: flex; align-items: center; gap: 8px; }
      .row .status { flex: 1; font-size: 12px; color: var(--mut); }
      .status.ok { color: var(--ok); } .status.err { color: var(--err); }
      [hidden] { display: none !important; }
    </style>
    <div class="ui">
      <button class="float pill" data-act="doc" title="Comment on the whole document"></button>
      <button class="float" data-act="sel" hidden>💬 Comment</button>
      <button class="float" data-act="sec" hidden>💬 Section</button>
      <div class="float panel" data-panel hidden>
        <div class="ctx" data-ctx></div>
        <div class="quote" data-quote hidden></div>
        <textarea data-text placeholder="Question or feedback…"></textarea>
        <div class="row">
          <span class="status" data-status></span>
          <button data-act="cancel">Cancel</button>
          <button class="primary" data-act="send">Send</button>
        </div>
      </div>
    </div>`;
  document.body.appendChild(host);
  const $ = (s) => shadow.querySelector(s);
  const pill = $('[data-act="doc"]'), selBtn = $('[data-act="sel"]'), secBtn = $('[data-act="sec"]');
  const panel = $("[data-panel]"), ctxEl = $("[data-ctx]"), quoteEl = $("[data-quote]");
  const text = $("[data-text]"), status = $("[data-status]"), sendBtn = $('[data-act="send"]');
  pill.textContent = `💬 → ${cfg.session}`;

  let current = null; // { quote, path, id, rect }
  let pendingSel = null; // { quote, section, range, rect }
  let pendingHeading = null;
  let hideTimer = 0;

  function place(el, rect, below = true) {
    const w = el.offsetWidth || 380, h = el.offsetHeight || 40;
    let left = Math.min(Math.max(8, rect.left), innerWidth - w - 8);
    let top = below ? rect.bottom + 8 : rect.top - h - 8;
    if (top + h > innerHeight - 8) top = Math.max(8, rect.top - h - 8);
    if (top < 8) top = 8;
    el.style.left = `${left}px`;
    el.style.top = `${top}px`;
  }

  function setStatus(msg, kind = "") {
    status.textContent = msg;
    status.className = `status ${kind}`;
  }

  function openComposer(ctx, rect) {
    current = ctx;
    selBtn.hidden = secBtn.hidden = true;
    ctxEl.innerHTML = "";
    const where = ctx.path || "Whole document";
    const b = document.createElement("b");
    b.textContent = where;
    ctxEl.append(ctx.quote ? "Selection in " : ctx.path ? "Section: " : "", b);
    quoteEl.hidden = !ctx.quote;
    quoteEl.textContent = ctx.quote || "";
    text.value = "";
    setStatus(`→ ${cfg.session}`);
    sendBtn.disabled = false;
    panel.hidden = false;
    place(panel, rect);
    text.focus();
  }

  function closeComposer() {
    panel.hidden = true;
    current = null;
    unhighlight();
  }

  async function send() {
    const comment = text.value.trim();
    if (!comment || !current || sendBtn.disabled) return;
    sendBtn.disabled = true;
    setStatus("Sending…");
    try {
      const res = await fetch("/__annotate/send", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Annotate-Token": cfg.token },
        body: JSON.stringify({
          quote: current.quote, sectionPath: current.path, sectionId: current.id, comment,
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok || !body.ok) throw new Error(body.error || `HTTP ${res.status}`);
      setStatus("Sent ✓", "ok");
      setTimeout(closeComposer, 700);
    } catch (e) {
      setStatus(String(e.message || e), "err");
      sendBtn.disabled = false;
    }
  }

  shadow.addEventListener("click", (e) => {
    const act = e.target.closest("[data-act]")?.dataset.act;
    if (act === "send") send();
    else if (act === "cancel") closeComposer();
    else if (act === "doc") {
      unhighlight();
      openComposer({ quote: "", path: "", id: "" }, pill.getBoundingClientRect());
    } else if (act === "sel" && pendingSel) {
      highlight(pendingSel.range);
      openComposer(
        { quote: pendingSel.quote, path: pendingSel.section.path, id: pendingSel.section.id },
        pendingSel.rect,
      );
    } else if (act === "sec" && pendingHeading) {
      const s = sectionOf(pendingHeading);
      highlight(sectionRange(pendingHeading));
      openComposer({ quote: "", path: s.path, id: s.id }, pendingHeading.getBoundingClientRect());
    }
  });

  text.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send(); }
    if (e.key === "Escape") closeComposer();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !panel.hidden) closeComposer();
  });

  const inOverlay = (e) => e.composedPath().includes(host);

  // Text selection -> floating "Comment" button (mouse or keyboard selection).
  function onSelect(e) {
    if (inOverlay(e)) return;
    setTimeout(() => {
      const sel = getSelection();
      const quote = sel && !sel.isCollapsed ? sel.toString().trim() : "";
      if (!quote || !root.contains(sel.anchorNode)) { selBtn.hidden = true; return; }
      const range = sel.getRangeAt(0).cloneRange();
      const rect = range.getBoundingClientRect();
      pendingSel = { quote, range, rect, section: sectionOf(range.startContainer) };
      selBtn.hidden = false;
      place(selBtn, rect);
    }, 0);
  }
  document.addEventListener("mouseup", onSelect);
  document.addEventListener("keyup", (e) => { if (e.shiftKey || e.key === "Shift") onSelect(e); });
  document.addEventListener("mousedown", (e) => {
    if (!inOverlay(e)) selBtn.hidden = true;
  });
  addEventListener("scroll", () => { selBtn.hidden = true; secBtn.hidden = true; }, { passive: true });

  // Heading hover -> "Section" button, for feedback with no text selected.
  root.addEventListener("mouseover", (e) => {
    const h = e.target.closest?.(HEADINGS);
    if (!h || !root.contains(h) || !panel.hidden) return;
    clearTimeout(hideTimer);
    pendingHeading = h;
    const r = h.getBoundingClientRect();
    secBtn.hidden = false;
    place(secBtn, { left: Math.min(r.right + 8, innerWidth - 110), top: r.top, bottom: r.top }, false);
    secBtn.style.top = `${Math.max(8, r.top + r.height / 2 - secBtn.offsetHeight / 2)}px`;
  });
  const scheduleHide = () => { hideTimer = setTimeout(() => { secBtn.hidden = true; }, 350); };
  root.addEventListener("mouseout", (e) => { if (e.target.closest?.(HEADINGS)) scheduleHide(); });
  secBtn.addEventListener("mouseenter", () => clearTimeout(hideTimer));
  secBtn.addEventListener("mouseleave", scheduleHide);
})();
