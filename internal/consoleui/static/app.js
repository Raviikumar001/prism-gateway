(() => {
  const TOKEN_KEY = "prism.ops.adminToken";
  const VK_KEY = "prism.ops.virtualKey";

  const el = (id) => document.getElementById(id);
  const tokenInput = el("admin-token");
  const keyInput = el("virtual-key");
  const statusEl = el("status");

  tokenInput.value = localStorage.getItem(TOKEN_KEY) || "dev-admin-change-me";
  keyInput.value = localStorage.getItem(VK_KEY) || "prism-sk-search-1a2b3c";

  function setStatus(msg, isErr) {
    statusEl.textContent = msg;
    statusEl.classList.toggle("err", !!isErr);
  }

  async function api(path) {
    const token = tokenInput.value.trim();
    const res = await fetch(path, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) {
      const body = await res.text();
      throw new Error(`${res.status} ${path}: ${body.slice(0, 160)}`);
    }
    return res.json();
  }

  function renderProviders(data) {
    const root = el("providers");
    const providers = data.providers || [];
    if (!providers.length) {
      root.innerHTML = `<p class="hint">No providers reported.</p>`;
      return;
    }
    root.innerHTML = providers
      .map((p) => {
        const state = (p.state || "unknown").toLowerCase();
        const rate = typeof p.failure_rate === "number" ? (p.failure_rate * 100).toFixed(0) + "%" : "—";
        return `<article class="card">
          <div class="name">${escapeHtml(p.name)}</div>
          <span class="badge ${escapeHtml(state)}">${escapeHtml(state)}</span>
          <div class="hint" style="margin:0">samples ${p.samples ?? 0} · failures ${p.failures ?? 0} · rate ${rate} · cooldown ${p.cooldown_ms ?? "—"}ms</div>
          <div class="hint" style="margin:0;font-family:var(--mono);font-size:0.72rem">${escapeHtml(p.base_url || "")}</div>
        </article>`;
      })
      .join("");
  }

  function renderCache(data) {
    const items = [
      ["Hit rate", pct(data.hit_rate)],
      ["Exact hits", data.hits_exact ?? 0],
      ["Semantic hits", data.hits_semantic ?? 0],
      ["Misses", data.misses ?? 0],
      ["Stores", data.stores ?? 0],
    ];
    el("cache").innerHTML = items
      .map(([k, v]) => `<div class="stat"><div class="k">${k}</div><div class="v">${v}</div></div>`)
      .join("");
  }

  function renderUsage(data) {
    const items = [
      ["Month", data.month ?? "—"],
      ["Requests", data.requests ?? 0],
      ["Prompt tok", data.prompt_tokens ?? 0],
      ["Completion tok", data.completion_tokens ?? 0],
      ["Cost USD", data.cost_usd ?? "0"],
      ["Cache hits", data.cache_hits ?? 0],
    ];
    el("usage").innerHTML = items
      .map(([k, v]) => `<div class="stat"><div class="k">${k}</div><div class="v">${escapeHtml(String(v))}</div></div>`)
      .join("");
  }

  function renderLogs(data) {
    const rows = data.logs || [];
    const tbody = el("logs");
    if (!rows.length) {
      tbody.innerHTML = `<tr><td colspan="8">No logs yet.</td></tr>`;
      return;
    }
    tbody.innerHTML = rows
      .map((r) => {
        const t = r.created_at ? new Date(r.created_at).toISOString().replace("T", " ").slice(0, 19) : "—";
        const prov = [r.resolved_provider, r.resolved_model].filter(Boolean).join("/") || "—";
        return `<tr>
          <td>${escapeHtml(t)}</td>
          <td>${escapeHtml(r.status || "")}</td>
          <td>${escapeHtml(r.requested_model || "")}</td>
          <td>${escapeHtml(prov)}</td>
          <td>${escapeHtml(r.cache || "")}</td>
          <td>${r.fallback ? "true" : "false"}</td>
          <td>${r.cost_micro_cents ?? 0}</td>
          <td>${r.latency_ms ?? 0}</td>
        </tr>`;
      })
      .join("");
  }

  function pct(n) {
    if (typeof n !== "number" || Number.isNaN(n)) return "—";
    return (n * 100).toFixed(1) + "%";
  }

  function escapeHtml(s) {
    return String(s)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  async function loadAll() {
    const key = keyInput.value.trim();
    localStorage.setItem(TOKEN_KEY, tokenInput.value.trim());
    localStorage.setItem(VK_KEY, key);
    setStatus("Loading…");
    try {
      const [providers, cache, usage, logs] = await Promise.all([
        api("/admin/providers/health"),
        api("/admin/cache/stats"),
        api(`/admin/usage?key=${encodeURIComponent(key)}`),
        api(`/admin/logs?key=${encodeURIComponent(key)}&limit=40`),
      ]);
      renderProviders(providers);
      renderCache(cache);
      renderUsage(usage);
      renderLogs(logs);
      setStatus(`Updated ${new Date().toLocaleTimeString()}`);
    } catch (err) {
      setStatus(String(err.message || err), true);
    }
  }

  el("auth-form").addEventListener("submit", (e) => {
    e.preventDefault();
    loadAll();
  });
  el("refresh").addEventListener("click", () => loadAll());

  loadAll();
  setInterval(loadAll, 15000);
})();
