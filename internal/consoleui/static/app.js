(() => {
  const TOKEN_KEY = "prism.ops.adminToken";
  const VK_KEY = "prism.ops.virtualKey";

  const el = (id) => document.getElementById(id);
  const tokenInput = el("admin-token");
  const keyInput = el("virtual-key");
  const statusEl = el("status");

  tokenInput.value = localStorage.getItem(TOKEN_KEY) || "";
  keyInput.value = localStorage.getItem(VK_KEY) || "prism-sk-search-1a2b3c";
  let logFilter = "all";
  let openLogId = "";

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
      ["From", data.from ?? "—"],
      ["To", data.to ?? "—"],
      ["Granularity", data.granularity ?? "month"],
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

  function statusTone(row) {
    const status = row.status || "";
    if (row.cache === "hit") return "ok";
    if (status === "ok") return "ok";
    if (status.startsWith("rejected_") || status === "client_abort") return "warn";
    if (status.includes("unavailable") || status.includes("error") || status === "gateway_overloaded") return "bad";
    return "muted";
  }

  function formatTime(iso) {
    if (!iso) return { rel: "—", utc: "" };
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return { rel: String(iso), utc: "" };
    const delta = Date.now() - d.getTime();
    const utc = d.toISOString().replace("T", " ").slice(0, 19) + " UTC";
    const sec = Math.max(0, Math.round(delta / 1000));
    let rel = `${sec}s ago`;
    if (sec >= 60 && sec < 3600) rel = `${Math.round(sec / 60)}m ago`;
    else if (sec >= 3600 && sec < 86400) rel = `${Math.round(sec / 3600)}h ago`;
    else if (sec >= 86400) rel = `${Math.round(sec / 86400)}d ago`;
    return { rel, utc };
  }

  function formatTokens(row) {
    const p = row.prompt_tokens || 0;
    const c = row.completion_tokens || 0;
    if (!p && !c) return "—";
    return `${p} → ${c}`;
  }

  function flagPills(row) {
    const pills = [];
    if (row.cache === "hit") pills.push(`<span class="pill hit">${escapeHtml(row.detail || "hit")}</span>`);
    if (row.fallback) pills.push(`<span class="pill warn">fallback</span>`);
    if (row.stream) pills.push(`<span class="pill">stream</span>`);
    if (row.cost_estimated) pills.push(`<span class="pill warn">est.</span>`);
    if (row.retries) pills.push(`<span class="pill">r×${row.retries}</span>`);
    return pills.length ? `<div class="pills">${pills.join("")}</div>` : `<span class="muted">—</span>`;
  }

  function renderLogs(data) {
    const rows = data.logs || [];
    const tbody = el("logs");
    if (!rows.length) {
      tbody.innerHTML = `<tr><td class="empty" colspan="8">No requests in this filter yet.</td></tr>`;
      return;
    }
    tbody.innerHTML = rows
      .map((r) => {
        const t = formatTime(r.created_at);
        const tone = statusTone(r);
        const label = r.status_label || r.status || "unknown";
        const upstream = [r.resolved_provider, r.resolved_model].filter(Boolean).join("/") || "—";
        const cost = r.cost_usd || "0";
        const open = r.request_id && r.request_id === openLogId;
        const detail = `
          <tr class="log-detail-row"${open ? "" : " hidden"}>
            <td colspan="8">
              <div class="log-detail">
                <div><div class="k">Request id</div><div class="v">${escapeHtml(r.request_id || "—")}</div></div>
                <div><div class="k">Virtual key</div><div class="v">${escapeHtml(r.virtual_key || "—")}</div></div>
                <div><div class="k">Route</div><div class="v">${escapeHtml(r.route_reason || "—")}</div></div>
                <div><div class="k">Detail</div><div class="v">${escapeHtml(r.detail || "—")}</div></div>
              </div>
            </td>
          </tr>`;
        return `<tr class="log-row${open ? " open" : ""}" data-id="${escapeHtml(r.request_id || "")}">
          <td title="${escapeHtml(t.utc)}">${escapeHtml(t.rel)}</td>
          <td><span class="badge ${tone}">${escapeHtml(label)}</span></td>
          <td><span class="trunc" title="${escapeHtml(r.requested_model || "")}">${escapeHtml(r.requested_model || "—")}</span></td>
          <td><span class="trunc" title="${escapeHtml(upstream)}">${escapeHtml(upstream)}</span></td>
          <td>${escapeHtml(formatTokens(r))}</td>
          <td>${escapeHtml(cost)}${r.cost_estimated ? " est." : ""}</td>
          <td>${flagPills(r)}</td>
          <td>${r.latency_ms ?? 0}</td>
        </tr>${detail}`;
      })
      .join("");

    tbody.querySelectorAll(".log-row").forEach((row) => {
      row.addEventListener("click", () => {
        const id = row.getAttribute("data-id") || "";
        openLogId = openLogId === id ? "" : id;
        tbody.querySelectorAll(".log-row").forEach((elRow) => {
          const match = elRow.getAttribute("data-id") === openLogId;
          elRow.classList.toggle("open", match);
          const detailRow = elRow.nextElementSibling;
          if (detailRow && detailRow.classList.contains("log-detail-row")) {
            detailRow.hidden = !match;
          }
        });
      });
    });
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
      const logsQS = new URLSearchParams({ key, limit: "80" });
      if (logFilter && logFilter !== "all") logsQS.set("status", logFilter);
      const [providers, cache, usage, logs] = await Promise.all([
        api("/admin/providers/health"),
        api("/admin/cache/stats"),
        api(`/admin/usage?key=${encodeURIComponent(key)}`),
        api(`/admin/logs?${logsQS.toString()}`),
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

  el("log-filters").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-filter]");
    if (!btn) return;
    logFilter = btn.getAttribute("data-filter") || "all";
    el("log-filters").querySelectorAll(".chip").forEach((chip) => {
      chip.classList.toggle("active", chip === btn);
    });
    loadAll();
  });

  el("auth-form").addEventListener("submit", (e) => {
    e.preventDefault();
    loadAll();
  });
  el("refresh").addEventListener("click", () => loadAll());

  loadAll();
  setInterval(loadAll, 15000);
})();
