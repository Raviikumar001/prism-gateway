(() => {
  const el = (id) => document.getElementById(id);
  const statusEl = el("status");
  const searchInput = el("search");

  let logFilter = "all";
  let openLogId = "";
  let pollTimer = 0;
  let latest = null;

  function setStatus(msg, isErr) {
    if (!msg) {
      statusEl.hidden = true;
      statusEl.textContent = "";
      return;
    }
    statusEl.hidden = false;
    statusEl.textContent = msg;
    statusEl.classList.toggle("err", !!isErr);
  }

  function escapeHtml(s) {
    return String(s)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  function pct(n, digits = 1) {
    if (typeof n !== "number" || Number.isNaN(n)) return "—";
    return (n * 100).toFixed(digits) + "%";
  }

  function money(v) {
    if (v == null || v === "") return "$0";
    const s = String(v);
    return s.startsWith("$") ? s : "$" + s;
  }

  function fmtInt(n) {
    return Number(n || 0).toLocaleString();
  }

  function fmtMs(n) {
    if (!n) return "—";
    return Math.round(n) + "ms";
  }

  function queryText() {
    return (searchInput.value || "").trim().toLowerCase();
  }

  function matchesQuery(parts) {
    const q = queryText();
    if (!q) return true;
    return parts.join(" ").toLowerCase().includes(q);
  }

  function formatTime(iso) {
    if (!iso) return { rel: "—", utc: "" };
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return { rel: String(iso), utc: "" };
    const sec = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000));
    const utc = d.toISOString().replace("T", " ").slice(0, 16) + " UTC";
    let rel = `${sec}s ago`;
    if (sec >= 60 && sec < 3600) rel = `${Math.round(sec / 60)}m ago`;
    else if (sec >= 3600 && sec < 86400) rel = `${Math.round(sec / 3600)}h ago`;
    else if (sec >= 86400) rel = `${Math.round(sec / 86400)}d ago`;
    return { rel, utc };
  }

  function statusTone(row) {
    const status = row.status || "";
    if (row.cache === "hit") return "ok";
    if (status === "ok") return "ok";
    if (status.startsWith("rejected_") || status === "client_abort") return "warn";
    if (status.includes("unavailable") || status.includes("error") || status === "gateway_overloaded") return "bad";
    return "muted";
  }

  function spendDelta(current, prior) {
    const a = Number(String(current || "0").replace("$", ""));
    const b = Number(String(prior || "0").replace("$", ""));
    if (!b && !a) return { cls: "", text: "" };
    if (!b) return { cls: "warn", text: "new" };
    const change = ((a - b) / b) * 100;
    const cls = change > 8 ? "warn" : change < -8 ? "ok" : "";
    const arrow = change >= 0 ? "▲" : "▼";
    return { cls, text: `${arrow} ${Math.abs(change).toFixed(0)}%` };
  }

  function renderHeader(data) {
    const s = data.summary || {};
    const health = data.health || {};
    const checks = health.checks || {};
    const mode = health.provider_mode || "live";
    el("env-chip").textContent = mode === "mocks" ? "Mocks" : "Production";
    const healthy = s.providers_healthy ?? 0;
    const total = s.providers_total ?? 0;
    const open = s.providers_open ?? 0;
    const half = s.providers_half ?? 0;
    el("header-pills").innerHTML = [
      `<span class="pill-dot ok"><i></i>${healthy}/${total} healthy</span>`,
      `<span class="pill-dot bad"><i></i>${open} failing</span>`,
      `<span class="pill-dot warn"><i></i>${half} stuck</span>`,
      `<span class="pill-dot"><i></i>24h spend ${escapeHtml(money(s.spend_24h_usd))}</span>`,
      `<span class="pill-dot"><i></i>success ${escapeHtml(pct(s.success_rate_24h))}</span>`,
      `<span class="pill-dot ${checks.postgres === "up" && checks.redis === "up" ? "ok" : "bad"}"><i></i>${checks.postgres === "up" && checks.redis === "up" ? "deps up" : "deps down"}</span>`,
    ].join("");
  }

  function renderMetrics(data) {
    const s = data.summary || {};
    const healthy = s.providers_healthy ?? 0;
    const total = s.providers_total ?? 0;
    const open = s.providers_open ?? 0;
    const half = s.providers_half ?? 0;
    el("m-healthy").textContent = `${healthy} / ${total}`;
    const hDelta = el("m-healthy-delta");
    hDelta.className = "delta " + (open ? "bad" : half ? "warn" : "ok");
    hDelta.textContent = open ? "failing" : half ? "probing" : "▲ stable";
    el("m-healthy-sub").innerHTML = `<span class="pill-dot bad"><i></i></span> `.repeat(open) + `<span class="pill-dot warn"><i></i></span>`.repeat(half);

    el("m-spend").textContent = money(s.spend_24h_usd);
    const d = spendDelta(s.spend_24h_usd, s.spend_prior_usd);
    const spendDeltaEl = el("m-spend-delta");
    spendDeltaEl.className = "delta " + d.cls;
    spendDeltaEl.textContent = d.text;
    el("m-spend-sub").textContent = `vs ${money(s.spend_prior_usd)} prior 24h`;

    const incidents = s.open_incidents ?? 0;
    el("m-incidents").textContent = String(incidents);
    const iDelta = el("m-incidents-delta");
    iDelta.className = "delta " + (incidents ? "bad" : "ok");
    iDelta.textContent = incidents ? "▲ open" : "clear";
  }

  function renderSuccess(data) {
    const s = data.summary || {};
    const ok = s.ok_24h || 0;
    const err = s.errors_24h || 0;
    const rej = s.rejected_24h || 0;
    const total = s.runs_24h || 0;
    el("success-v").textContent = total ? pct(s.success_rate_24h) : "—";
    const delta = el("success-delta");
    const prior = s.success_rate_prior || 0;
    const curr = s.success_rate_24h || 0;
    if (!total) {
      delta.className = "delta";
      delta.textContent = "no traffic";
    } else if (prior && curr + 0.01 < prior) {
      delta.className = "delta bad";
      delta.textContent = "▼ Degrading";
    } else {
      delta.className = "delta ok";
      delta.textContent = "▲ stable";
    }
    el("success-hint").textContent = `Completed runs / total runs · ${fmtInt(total)} runs`;
    const okPct = total ? (ok / total) * 100 : 0;
    const errPct = total ? (err / total) * 100 : 0;
    const rejPct = total ? (rej / total) * 100 : 0;
    el("bar-ok").style.width = okPct + "%";
    el("bar-bad").style.width = errPct + "%";
    el("bar-warn").style.width = rejPct + "%";
    el("bar-legend").innerHTML = `
      <span>${fmtInt(ok)} succeeded</span>
      <span>${fmtInt(err)} failed</span>
      <span>${fmtInt(rej)} rejected</span>`;
    const note = el("success-note");
    if (!total) {
      note.className = "callout";
      note.textContent = "No requests in the last 24 hours yet.";
    } else if (err) {
      note.className = "callout bad";
      note.textContent = `${fmtInt(err)} upstream/gateway errors in 24h. Open the request log for route reason and detail.`;
    } else if (rej) {
      note.className = "callout";
      note.textContent = `${fmtInt(rej)} admits were rejected by RPM, TPM, budget, or allowlist.`;
    } else {
      note.className = "callout ok";
      note.textContent = "Success is holding. Cache hit rate " + pct(s.cache_hit_rate) + " this process.";
    }
  }

  function renderRunMix(data) {
    const rows = data.run_mix || [];
    el("run-mix").innerHTML = rows
      .map((r) => `<tr>
        <td><span class="state ${escapeHtml(r.tone || "")}"><i></i>${escapeHtml(r.label)}</span></td>
        <td>${fmtInt(r.providers)}</td>
        <td>${fmtInt(r.runs)}</td>
        <td>${fmtMs(r.latency_ms)}</td>
        <td>${escapeHtml(money(r.cost_usd))}</td>
      </tr>`)
      .join("");
  }

  function renderAttention(data) {
    const items = data.attention || [];
    el("attention-list").innerHTML = items
      .map((item, i) => {
        const tone = item.tone === "warning" ? "warn" : item.tone === "success" ? "success" : "";
        const cta = item.href
          ? `<a class="${tone}" href="${escapeHtml(item.href)}">${item.tone === "success" ? "View providers" : "Open details"} →</a>`
          : "";
        return `<li>
          <div class="n">${String(i + 1).padStart(2, "0")}</div>
          <div>
            <div class="t">${escapeHtml(item.title)}</div>
            <p class="b">${escapeHtml(item.body)}</p>
            ${cta}
          </div>
        </li>`;
      })
      .join("");
  }

  function renderChart(series) {
    const root = el("chart");
    const points = series || [];
    if (!points.length) {
      root.innerHTML = `<p class="hint">No spend yet in this window.</p>`;
      return;
    }
    const w = 640;
    const h = 180;
    const pad = { l: 8, r: 8, t: 12, b: 22 };
    const max = Math.max(...points.map((p) => Number(p.cost_usd) || 0), 0.000001);
    const innerW = w - pad.l - pad.r;
    const innerH = h - pad.t - pad.b;
    const coords = points.map((p, i) => {
      const x = pad.l + (points.length === 1 ? innerW / 2 : (i / (points.length - 1)) * innerW);
      const y = pad.t + innerH - ((Number(p.cost_usd) || 0) / max) * innerH;
      return { x, y, p };
    });
    const line = coords.map((c, i) => `${i ? "L" : "M"}${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(" ");
    const fill = `M${coords[0].x},${pad.t + innerH} ` +
      coords.map((c) => `L${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(" ") +
      ` L${coords[coords.length - 1].x},${pad.t + innerH} Z`;
    const peak = coords.reduce((a, b) => (Number(b.p.cost_usd) > Number(a.p.cost_usd) ? b : a), coords[0]);
    const peakLabel = new Date(peak.p.hour).toISOString().slice(11, 16) + " UTC";
    root.innerHTML = `<div style="position:relative;height:180px">
      <svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none">
        <defs>
          <linearGradient id="spendFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#8b7cf6" stop-opacity="0.35"/>
            <stop offset="100%" stop-color="#8b7cf6" stop-opacity="0"/>
          </linearGradient>
        </defs>
        <path d="${fill}" fill="url(#spendFill)"></path>
        <path d="${line}" fill="none" stroke="#8b7cf6" stroke-width="2.2"></path>
        <circle cx="${peak.x}" cy="${peak.y}" r="4" fill="#c4b5fd"></circle>
      </svg>
      <div class="chart-tip" style="left:${(peak.x / w) * 100}%;top:${(peak.y / h) * 100}%">${escapeHtml(peakLabel)} · ${escapeHtml(money(Number(peak.p.cost_usd).toFixed(4)))}</div>
    </div>`;
  }

  function tenantBadge(t) {
    const used = t.budget_used_frac || 0;
    if (used >= 1) return { cls: "bad", text: "Over budget" };
    if (used >= 0.8) return { cls: "warn", text: "Cost spike" };
    if ((t.status || "") !== "active") return { cls: "warn", text: t.status };
    return { cls: "ok", text: "Healthy" };
  }

  function renderTenants(data) {
    const q = queryText();
    let tenants = data.tenants || [];
    if (q.includes("budget") || q.includes("exceed")) {
      tenants = [...tenants].sort((a, b) => (b.budget_used_frac || 0) - (a.budget_used_frac || 0));
    }
    tenants = tenants.filter((t) => matchesQuery([t.team, t.status, t.spend_usd]));
    if (!tenants.length) {
      el("tenant-list").innerHTML = `<li class="hint">No tenants match.</li>`;
      return;
    }
    el("tenant-list").innerHTML = tenants
      .map((t) => {
        const badge = tenantBadge(t);
        const initial = String(t.team || "?").slice(0, 1).toUpperCase();
        const used = Math.round((t.budget_used_frac || 0) * 100);
        return `<li>
          <div class="avatar">${escapeHtml(initial)}</div>
          <div class="who">
            <div class="name">${escapeHtml(t.team)}</div>
            <div class="meta">${escapeHtml(money(t.spend_usd))} of $${Number(t.budget_usd || 0)} · ${used}% used · ${fmtInt(t.requests)} req</div>
          </div>
          <span class="badge ${badge.cls}">${escapeHtml(badge.text)}</span>
        </li>`;
      })
      .join("");
  }

  function renderLogs(data) {
    const q = queryText();
    const rows = (data.logs || []).filter((r) =>
      matchesQuery([r.team, r.status, r.status_label, r.requested_model, r.resolved_provider, r.resolved_model, r.detail, r.route_reason])
    );
    const tbody = el("logs-body");
    if (!rows.length) {
      tbody.innerHTML = `<tr><td class="empty" colspan="8">${q ? "No requests match that search." : "No requests in this filter yet."}</td></tr>`;
      return;
    }
    tbody.innerHTML = rows
      .map((r) => {
        const t = formatTime(r.created_at);
        const tone = statusTone(r);
        const label = r.status_label || r.status || "unknown";
        const upstream = [r.resolved_provider, r.resolved_model].filter(Boolean).join("/") || "—";
        const tokens = (r.prompt_tokens || r.completion_tokens)
          ? `${r.prompt_tokens || 0} → ${r.completion_tokens || 0}`
          : "—";
        const open = r.request_id && r.request_id === openLogId;
        const detail = `<tr class="log-detail-row"${open ? "" : " hidden"}>
          <td colspan="8"><div class="log-detail">
            <div><div class="k">Request id</div><div class="v">${escapeHtml(r.request_id || "—")}</div></div>
            <div><div class="k">Team</div><div class="v">${escapeHtml(r.team || "—")}</div></div>
            <div><div class="k">Route</div><div class="v">${escapeHtml(r.route_reason || "—")}</div></div>
            <div><div class="k">Detail</div><div class="v">${escapeHtml(r.detail || "—")}</div></div>
          </div></td></tr>`;
        return `<tr class="log-row${open ? " open" : ""}" data-id="${escapeHtml(r.request_id || "")}">
          <td title="${escapeHtml(t.utc)}">${escapeHtml(t.rel)}</td>
          <td>${escapeHtml(r.team || "—")}</td>
          <td><span class="badge ${tone}">${escapeHtml(label)}</span></td>
          <td><span class="trunc" title="${escapeHtml(r.requested_model || "")}">${escapeHtml(r.requested_model || "—")}</span></td>
          <td><span class="trunc">${escapeHtml(upstream)}</span></td>
          <td>${escapeHtml(tokens)}</td>
          <td>${escapeHtml(money(r.cost_usd))}${r.cost_estimated ? " est." : ""}</td>
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

  function renderAll(data) {
    latest = data;
    renderHeader(data);
    renderMetrics(data);
    renderSuccess(data);
    renderRunMix(data);
    renderAttention(data);
    renderChart(data.spend_24h || []);
    renderTenants(data);
    renderLogs(data);
  }

  async function loadAll() {
    setStatus("Loading…");
    try {
      const qs = new URLSearchParams();
      if (logFilter && logFilter !== "all") qs.set("status", logFilter);
      const res = await fetch(`/console/api/overview?${qs.toString()}`);
      if (!res.ok) throw new Error(`Could not load overview (${res.status})`);
      const data = await res.json();
      renderAll(data);
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

  el("refresh").addEventListener("click", () => loadAll());
  searchInput.addEventListener("input", () => {
    if (latest) {
      renderTenants(latest);
      renderLogs(latest);
    }
  });
  window.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      searchInput.focus();
    }
  });

  pollTimer = setInterval(loadAll, 15000);
  loadAll();
})();
