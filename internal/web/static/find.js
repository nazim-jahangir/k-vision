// Find in the drawer's YAML: ⌘F / Ctrl+F while the YAML view, the Edit tab or
// "+ Create" is open. Enter = next, ⇧Enter = previous, Esc = close.
//
// The editor is a <textarea>, which cannot colour parts of its text, and its own
// selection is invisible while the find box has the focus. So matches are drawn on a
// layer behind the textarea (same font, padding and scroll; the textarea is made
// transparent on top). The read-only YAML view is a <pre>: matches are wrapped in
// <mark> there directly. Text comes from the cluster, so everything is text nodes.
(function () {
  let bar = null; // { el, input, count, close() }

  function mk(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (k === "text") e.textContent = v;
      else if (k.startsWith("on")) e[k] = v;
      else e.setAttribute(k, v);
    }
    for (const c of kids) if (c != null) e.append(c);
    return e;
  }

  function matches(text, q) {
    const out = [];
    if (!q) return out;
    const t = text.toLowerCase(), n = q.toLowerCase();
    for (let i = t.indexOf(n); i !== -1 && out.length < 5000; i = t.indexOf(n, i + n.length)) out.push([i, i + n.length]);
    return out;
  }

  // ------------------------------------------------------------ textarea
  function textareaTarget(ta) {
    let host = ta.parentElement;
    if (!host.classList.contains("find-host")) {
      host = mk("div", { class: "find-host" });
      ta.replaceWith(host);
      host.append(mk("div", { class: "find-hl", "aria-hidden": "true" }), ta);
      ta.addEventListener("scroll", () => { const hl = host.firstChild; hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft; });
    }
    host.classList.add("on");
    const hl = host.firstChild;
    let ms = [];
    const draw = (q, cur) => {
      const text = ta.value;
      ms = matches(text, q);
      const frag = document.createDocumentFragment();
      let at = 0;
      ms.forEach(([s, e], i) => {
        frag.append(text.slice(at, s), mk("mark", { class: i === cur ? "cur" : "", text: text.slice(s, e) }));
        at = e;
      });
      frag.append(text.slice(at), "\n "); // a trailing newline needs a line to sit on
      hl.replaceChildren(frag);
      hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft;
      return ms.length;
    };
    return {
      draw,
      show(i) {
        const m = hl.querySelectorAll("mark")[i];
        if (!m) return;
        if (m.offsetTop < ta.scrollTop || m.offsetTop + m.offsetHeight > ta.scrollTop + ta.clientHeight) ta.scrollTop = m.offsetTop - ta.clientHeight / 3;
        if (m.offsetLeft < ta.scrollLeft || m.offsetLeft + m.offsetWidth > ta.scrollLeft + ta.clientWidth) ta.scrollLeft = Math.max(0, m.offsetLeft - 40);
        hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft;
        ta.setSelectionRange(ms[i][0], ms[i][1]);
      },
      // Esc puts the cursor on the match, ready to type over it.
      done(i) { host.classList.remove("on"); hl.replaceChildren(); ta.focus(); if (ms[i]) ta.setSelectionRange(ms[i][0], ms[i][1]); },
      watch(fn) { ta.addEventListener("input", fn); return () => ta.removeEventListener("input", fn); },
    };
  }

  // ------------------------------------------------------------ <pre>
  function preTarget(pre) {
    const clear = () => {
      for (const m of pre.querySelectorAll("mark.find")) m.replaceWith(m.textContent);
      pre.normalize();
    };
    return {
      draw(q, cur) {
        clear();
        const nodes = [];
        let text = "";
        const walk = document.createTreeWalker(pre, NodeFilter.SHOW_TEXT);
        for (let n = walk.nextNode(); n; n = walk.nextNode()) { nodes.push([n, text.length]); text += n.data; }
        const ms = matches(text, q);
        // A match can cross the colouring spans (key: value), so each text node gets
        // the parts of matches inside it, wrapped from its end so offsets stay valid.
        let k = 0;
        for (const [node, start] of nodes) {
          const end = start + node.data.length, parts = [];
          while (k < ms.length && ms[k][1] <= start) k++;
          for (let j = k; j < ms.length && ms[j][0] < end; j++) parts.push([Math.max(ms[j][0], start) - start, Math.min(ms[j][1], end) - start, j]);
          for (let p = parts.length - 1; p >= 0; p--) {
            const [s, e, j] = parts[p];
            const mid = node.splitText(s);
            mid.splitText(e - s);
            mid.replaceWith(mk("mark", { class: "find" + (j === cur ? " cur" : ""), "data-i": String(j), text: mid.data }));
          }
        }
        return ms.length;
      },
      show(i) { const m = pre.querySelector(`mark.find[data-i="${i}"]`); if (m) m.scrollIntoView({ block: "center", inline: "nearest" }); },
      done() { clear(); },
      watch() { return () => {}; },
    };
  }

  function open() {
    const body = document.getElementById("panelBody");
    if (!body || document.getElementById("panel").hidden) return false;
    const ta = body.querySelector("textarea.code");
    const pre = ta ? null : body.querySelector(".pane:not(.logs) > pre"); // logs have their own find box
    if (!ta && !pre) return false;
    if (bar) { bar.input.focus(); bar.input.select(); return true; }

    const target = ta ? textareaTarget(ta) : preTarget(pre);
    const input = mk("input", { id: "findText", type: "search", placeholder: "Find", "aria-label": "Find in YAML", autocomplete: "off", spellcheck: "false" });
    const count = mk("span", { class: "find-count muted", "aria-live": "polite" });
    let cur = 0, total = 0;
    const update = (move) => {
      const q = input.value;
      if (move) cur = total ? (cur + move + total) % total : 0;
      total = target.draw(q, cur);
      if (cur >= total) { cur = Math.max(0, total - 1); total = target.draw(q, cur); }
      count.textContent = !q ? "" : total ? `${cur + 1} of ${total}${total >= 5000 ? "+" : ""}` : "No matches";
      count.classList.toggle("none", !!q && !total);
      if (total) target.show(cur);
    };
    const btn = (label, title, fn) => mk("button", { type: "button", class: "ghost small", title, "aria-label": title, text: label, onclick: fn });
    const el = mk("div", { class: "find-bar", role: "search" }, input, count,
      btn("↑", "Previous match (⇧Enter)", () => update(-1)),
      btn("↓", "Next match (Enter)", () => update(1)),
      btn("✕", "Close (Esc)", () => close()));
    let timer;
    input.oninput = () => { cur = 0; update(0); };
    input.onkeydown = (e) => {
      if (e.key === "Enter") { e.preventDefault(); update(e.shiftKey ? -1 : 1); }
      else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); }
    };
    // Typing in the editor while the bar is open keeps the matches in place.
    const unwatch = target.watch(() => { clearTimeout(timer); timer = setTimeout(() => { if (input.value) { total = target.draw(input.value, cur); count.textContent = total ? `${Math.min(cur, total - 1) + 1} of ${total}` : "No matches"; } }, 150); });
    function close() {
      clearTimeout(timer); unwatch();
      el.remove();
      if (bar && bar.el === el) bar = null;
      target.done(cur);
      observer.disconnect();
    }
    // The drawer re-renders its body on every tab or object change: drop the bar with it.
    const observer = new MutationObserver(() => { if (!el.isConnected || !(ta || pre).isConnected) close(); });
    observer.observe(body, { childList: true, subtree: true });

    const pane = (ta || pre).closest(".pane") || body;
    const tools = pane.querySelector(":scope > .tools");
    if (tools) tools.after(el); else pane.prepend(el);
    bar = { el, input, close };
    // Start from the text already selected in the editor, like a text editor does.
    const sel = ta ? ta.value.slice(ta.selectionStart, ta.selectionEnd) : String(window.getSelection() || "");
    if (sel && !sel.includes("\n") && sel.length < 200) input.value = sel;
    input.focus(); input.select();
    if (input.value) update(0);
    return true;
  }

  window.KCFind = { open, close: () => bar && bar.close(), isOpen: () => !!bar };
})();
