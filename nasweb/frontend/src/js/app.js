// App SPA con Preact + HTM (no build step). Footprint del bundle ridotto.
// Gli specifier "bare" sono risolti dall'import map in index.html verso i file
// vendored localmente in /assets/vendor (nessuna dipendenza da CDN esterne).
import { h, render } from "preact";
import { useState, useEffect, useCallback } from "preact/hooks";
import htm from "htm";

import { t, getLocale, setLocale } from "./i18n.js";
import { api, setCsrfToken, connectWS, uploadFiles, downloadUrl } from "./api.js";

const html = htm.bind(h);

function App() {
  const [user, setUser] = useState(null);
  const [route, setRoute] = useState("raid");
  const [, force] = useState(0);
  const [qbt, setQbt] = useState(null); // stato app qBittorrent (per il gating del menu)

  // Re-render al cambio lingua.
  useEffect(() => {
    const onLocale = () => force((n) => n + 1);
    window.addEventListener("locale-changed", onLocale);
    const onExpired = () => setUser(null);
    window.addEventListener("session-expired", onExpired);
    return () => {
      window.removeEventListener("locale-changed", onLocale);
      window.removeEventListener("session-expired", onExpired);
    };
  }, []);

  // Verifica sessione esistente all'avvio.
  useEffect(() => {
    api.me()
      .then((me) => {
        setCsrfToken(me.csrf_token);
        setUser(me);
      })
      .catch(() => setUser(null));
  }, []);

  // Stato app qBittorrent: polling leggero per riflettere gating quando i volumi
  // cambiano (creati/rimossi a caldo).
  const refreshQbt = useCallback(() => {
    api.qbtStatus().then(setQbt).catch(() => {});
  }, []);
  useEffect(() => {
    if (!user) return;
    refreshQbt();
    const id = setInterval(refreshQbt, 8000);
    return () => clearInterval(id);
  }, [user, refreshQbt]);

  if (!user) {
    return html`<${Login} onLogin=${setUser} />`;
  }

  return html`
    <div class="layout">
      <${Sidebar} route=${route} setRoute=${setRoute} user=${user} qbt=${qbt} onLogout=${() => {
        api.logout().finally(() => setUser(null));
      }} />
      <main class="content">
        ${route === "raid" && html`<${RaidView} />`}
        ${route === "users" && html`<${UsersView} isAdmin=${user.is_admin} />`}
        ${route === "shares" && html`<${SharesView} isAdmin=${user.is_admin} />`}
        ${route === "files" && html`<${FilesView} isAdmin=${user.is_admin} />`}
        ${route === "dlna" && html`<${DlnaView} isAdmin=${user.is_admin} />`}
        ${route === "qbt" && html`<${QbtView} isAdmin=${user.is_admin} qbt=${qbt} onChanged=${refreshQbt} />`}
        ${route === "admin" && html`<${AdminView} isAdmin=${user.is_admin} />`}
      </main>
    </div>
  `;
}

function LangSwitcher() {
  const locale = getLocale();
  return html`
    <div class="lang">
      <button class=${locale === "it" ? "active" : ""} onClick=${() => setLocale("it")}>IT</button>
      <button class=${locale === "en" ? "active" : ""} onClick=${() => setLocale("en")}>EN</button>
    </div>
  `;
}

function Login({ onLogin }) {
  const [u, setU] = useState("");
  const [p, setP] = useState("");
  const [err, setErr] = useState("");

  const submit = async () => {
    setErr("");
    try {
      const res = await api.login(u, p);
      setCsrfToken(res.csrf_token);
      onLogin(res);
    } catch (_) {
      setErr(t("login.error"));
    }
  };

  return html`
    <div class="login-wrap">
      <div class="login-card">
        <${LangSwitcher} />
        <img class="login-logo" src="assets/img/logo.png" alt="ollozunaOS" />
        <h1>${t("login.title")}</h1>
        <label>${t("login.username")}
          <input value=${u} onInput=${(e) => setU(e.target.value)} />
        </label>
        <label>${t("login.password")}
          <input type="password" value=${p} onInput=${(e) => setP(e.target.value)}
                 onKeyDown=${(e) => e.key === "Enter" && submit()} />
        </label>
        ${err && html`<p class="error">${err}</p>`}
        <button class="primary" onClick=${submit}>${t("login.submit")}</button>
      </div>
    </div>
  `;
}

function Sidebar({ route, setRoute, user, qbt, onLogout }) {
  const items = ["raid", "users", "shares", "files", "dlna"];
  const qbtUnavailable = !qbt || qbt.state === "unavailable";
  return html`
    <aside class="sidebar">
      <div class="brand"><img class="brand-icon" src="assets/img/logo-icon.png" alt="" /><span class="brand-text">OLLOZUNA<br/>NAS Manager</span></div>
      <nav>
        ${items.map((it) => html`
          <button class=${route === it ? "nav-item active" : "nav-item"}
                  onClick=${() => setRoute(it)}>${t(`nav.${it}`)}</button>
        `)}
        <button class=${"nav-item" + (route === "qbt" ? " active" : "") + (qbtUnavailable ? " disabled" : "")}
                disabled=${qbtUnavailable}
                title=${qbtUnavailable ? t("qbt.tooltipUnavailable") : ""}
                onClick=${() => { if (!qbtUnavailable) setRoute("qbt"); }}>
          ${t("nav.qbt")}
        </button>
        ${user.is_admin && html`
          <button class=${route === "admin" ? "nav-item active" : "nav-item"}
                  onClick=${() => setRoute("admin")}>${t("nav.admin")}</button>
        `}
      </nav>
      <div class="sidebar-footer">
        <${LangSwitcher} />
        <div class="who">${user.username}${user.is_admin ? " (admin)" : ""}</div>
        <button class="link" onClick=${onLogout}>${t("app.logout")}</button>
      </div>
    </aside>
  `;
}

// ---- RAID helpers ----
function calcRaidCapacity(level, n, minSizeBytes) {
  if (n === 0 || minSizeBytes === 0) return 0;
  switch (String(level)) {
    case "0":      return minSizeBytes * n;
    case "1":      return minSizeBytes;
    case "5":      return n >= 3 ? minSizeBytes * (n - 1) : 0;
    case "6":      return n >= 4 ? minSizeBytes * (n - 2) : 0;
    case "10":     return n >= 4 ? minSizeBytes * Math.floor(n / 2) : 0;
    case "linear": return minSizeBytes * n;
    default:       return minSizeBytes;
  }
}

function RaidView() {
  const [arrays, setArrays] = useState(null);
  const [disks, setDisks] = useState(null);
  const [filesystems, setFilesystems] = useState(null);
  const [err, setErr] = useState("");
  // failedMap: { mdDevice: ["/dev/sdb", ...] }
  const [failedMap, setFailedMap] = useState({});
  // statusMap: { mdDevice: { phase: "expanding"|"rebuilding", progress: 0-100 } }
  const [statusMap, setStatusMap] = useState({});
  // addedDisksMap: { mdDevice: ["/dev/sdd"] } dischi aggiunti via expand
  const [addedDisksMap, setAddedDisksMap] = useState({});
  const [modal, setModal] = useState(null);

  const load = useCallback(() => {
    setErr("");
    api.listArrays().then(setArrays).catch((e) => setErr(e.message));
    api.listDisks().then(setDisks).catch((e) => setErr(e.message));
    api.listFilesystems().then((fs) => setFilesystems(Array.isArray(fs) ? fs : [])).catch(() => setFilesystems([]));
  }, []);

  useEffect(() => {
    load();
    const ws = connectWS((type) => { if (type === "raid.status") load(); });
    return () => ws.close();
  }, [load]);

  const allDisks = disks || [];

  const enrichedArrays = (arrays || []).map((a) => {
    const added = addedDisksMap[a.device] || [];
    const devices = [...(a.devices || []), ...added];
    const failedSet = new Set(failedMap[a.device] || []);
    const ovr = statusMap[a.device];
    const hasFailed = failedSet.size > 0;
    const displayState = ovr ? ovr.phase : (hasFailed ? "degraded" : a.state);
    return { ...a, devices, failedDevices: failedSet, displayState, progress: ovr?.progress };
  });

  const usedDevices = new Set(enrichedArrays.flatMap((a) => a.devices));
  const allFailedDevices = new Set(Object.values(failedMap).flat());
  const availableDisks = allDisks.filter((d) => !usedDevices.has(d.device) && !allFailedDevices.has(d.device));
  const failedDisks = allDisks.filter((d) => allFailedDevices.has(d.device));

  const startProgress = (mdDevice, phase, durationMs, onDone) => {
    let step = 0;
    const interval = 200;
    const total = durationMs / interval;
    const timer = setInterval(() => {
      step++;
      const pct = Math.min(99, Math.round((step / total) * 100));
      setStatusMap((p) => ({ ...p, [mdDevice]: { phase, progress: pct } }));
      if (step >= total) {
        clearInterval(timer);
        setStatusMap((p) => { const n = { ...p }; delete n[mdDevice]; return n; });
        onDone();
      }
    }, interval);
  };

  const handleExpand = (array, newDevices) => {
    setModal(null);
    Promise.all(newDevices.map((d) => api.addDisk(array.device, d))).catch((e) => setErr(e.message));
    setAddedDisksMap((p) => ({ ...p, [array.device]: [...(p[array.device] || []), ...newDevices] }));
    startProgress(array.device, "expanding", 5000, load);
  };

  const handleMarkFailed = (array, diskDevice) => {
    setModal(null);
    api.removeDisk(array.device, diskDevice).catch((e) => setErr(e.message));
    setFailedMap((p) => ({ ...p, [array.device]: [...(p[array.device] || []), diskDevice] }));
  };

  const handleRebuild = (array, failedDisk, newDisk) => {
    setModal(null);
    api.addDisk(array.device, newDisk).catch((e) => setErr(e.message));
    setFailedMap((p) => ({ ...p, [array.device]: (p[array.device] || []).filter((d) => d !== failedDisk) }));
    setAddedDisksMap((p) => ({ ...p, [array.device]: [...(p[array.device] || []), newDisk] }));
    startProgress(array.device, "rebuilding", 8000, load);
  };

  const handleWipeDisk = async (device) => {
    setErr("");
    try { await api.wipeDisk(device); setModal(null); load(); }
    catch (e) { setErr(e.message); }
  };

  const handleDelete = async (array) => {
    const fs = (filesystems || []).find((f) => f.device === array.device);
    let msg;
    if (fs && fs.is_mounted) {
      msg = t("raid.confirmDeleteMounted")
        .replace("{md}", array.device)
        .replace("{mp}", fs.mount_point)
        .replace("{fs}", fs.fstype || "?");
    } else if (fs && fs.fstype) {
      msg = t("raid.confirmDeleteFs")
        .replace("{md}", array.device)
        .replace("{fs}", fs.fstype);
    } else {
      msg = t("raid.confirmDelete").replace("{md}", array.device);
    }
    if (!window.confirm(msg)) return;
    try {
      await api.deleteArray(array.device);
      load();
    } catch (e) { setErr(e.message); }
  };

  return html`
    <section class="raid-view">
      <header class="page-head">
        <h1>${t("raid.title")}</h1>
        <button class="primary" onClick=${() => setModal({ type: "create" })}>${t("raid.create")}</button>
      </header>
      ${err && html`<p class="error">${err}</p>`}

      <h2 class="section-title">${t("raid.availableDisks")}</h2>
      ${disks === null && html`<p class="muted">${t("common.loading")}</p>`}

      ${failedDisks.length > 0 && html`
        <div class="failed-section">
          <p class="failed-label">⚠️ ${t("raid.failedDisksSection")}</p>
          <table class="data">
            <thead><tr>
              <th>${t("raid.diskDevice")}</th><th>${t("raid.diskSize")}</th>
              <th>${t("raid.diskModel")}</th><th>${t("raid.diskStatus")}</th>
            </tr></thead>
            <tbody>
              ${failedDisks.map((d) => html`
                <tr>
                  <td class="mono">${d.device}</td>
                  <td>${fmtBytes(d.size_bytes)}</td>
                  <td>${d.model || "—"}</td>
                  <td><span class="badge failed">❌ ${t("raid.diskFailed")}</span></td>
                </tr>
              `)}
            </tbody>
          </table>
        </div>
      `}

      ${disks !== null && availableDisks.length === 0 && failedDisks.length === 0 && html`<p class="muted">${t("raid.noDisksAvailable")}</p>`}
      ${availableDisks.length > 0 && html`
        <table class="data">
          <thead><tr>
            <th>${t("raid.diskDevice")}</th><th>${t("raid.diskSize")}</th>
            <th>${t("raid.diskType")}</th><th>${t("raid.diskModel")}</th><th>${t("raid.diskStatus")}</th>
            <th>${t("raid.actions") || "Actions"}</th>
          </tr></thead>
          <tbody>
            ${availableDisks.map((d) => html`
              <tr>
                <td class="mono">${d.device}</td>
                <td>${fmtBytes(d.size_bytes)}</td>
                <td>${d.rotational ? "HDD" : "SSD"}</td>
                <td>${d.model || "—"}</td>
                <td><span class="badge active">${t("raid.diskFree")}</span></td>
                <td class="actions-cell">
                  <button class="action-btn danger-btn" onClick=${() => setModal({ type:"wipeDisk", device: d.device })}>🧹 Wipe</button>
                </td>
              </tr>
            `)}
          </tbody>
        </table>
      `}

      <h2 class="section-title" style="margin-top:28px">${t("raid.existingArrays")}</h2>
      ${arrays === null && html`<p class="muted">${t("common.loading")}</p>`}
      ${arrays !== null && enrichedArrays.length === 0 && html`<p class="muted">${t("raid.empty")}</p>`}
      ${enrichedArrays.length > 0 && html`
        <div class="table-wrap">
          <table class="data">
            <thead><tr>
              <th>${t("raid.arrays")}</th><th>${t("raid.level")}</th>
              <th>${t("raid.disksUsed")}</th><th>${t("raid.totalSize")}</th>
              <th>${t("raid.state")}</th><th>${t("raid.health")}</th>
              <th>${t("raid.actions")}</th>
            </tr></thead>
            <tbody>
              ${enrichedArrays.map((a) => {
                const memberObjs = allDisks.filter((d) => a.devices.includes(d.device));
                const sizes = memberObjs.map((d) => d.size_bytes).filter((s) => s > 0);
                const minSize = sizes.length > 0 ? Math.min(...sizes) : 0;
                const totalSize = calcRaidCapacity(a.level, a.devices.length, minSize);
                const hasFailed = a.failedDevices.size > 0;
                const busy = a.displayState === "expanding" || a.displayState === "rebuilding";
                const healthy = a.displayState === "clean" || a.displayState === "active";

                const disksCell = a.devices.map((dev) => {
                  const fail = a.failedDevices.has(dev);
                  return html`<span class="disk-tag ${fail ? "disk-fail" : "disk-ok"}">
                    ${fail ? "❌" : "✅"} ${dev.replace("/dev/","")}
                  </span>`;
                });

                const stateLabel = a.displayState === "expanding"
                  ? html`<span class="badge expanding">Expanding ${a.progress||0}%</span>`
                  : a.displayState === "rebuilding"
                  ? html`<span class="badge rebuilding">Rebuilding ${a.progress||0}%</span>`
                  : html`<span class="badge ${a.displayState}">${a.displayState}</span>`;

                return html`
                  <tr>
                    <td class="mono">${a.device}</td>
                    <td>RAID ${a.level}</td>
                    <td class="disks-cell">${disksCell}</td>
                    <td>${totalSize ? fmtBytes(totalSize) : "—"}</td>
                    <td>
                      ${stateLabel}
                      ${busy && html`<div class="inline-bar"><div class="inline-bar-fill" style="width:${a.progress||0}%"></div></div>`}
                    </td>
                    <td>${healthy ? t("raid.healthy") : hasFailed ? "⚠️ Degraded" : "⚠️ " + a.displayState}</td>
                    <td class="actions-cell">
                      <button class="action-btn" disabled=${busy} onClick=${() => setModal({ type:"expand", array:a })}>
                        🔧 ${t("raid.expand")}
                      </button>
                      <button class="action-btn warn-btn" disabled=${busy || hasFailed} onClick=${() => setModal({ type:"markFailed", array:a })}>
                        ⚠️ ${t("raid.markFailed")}
                      </button>
                      <button class="action-btn" disabled=${!hasFailed || busy} onClick=${() => setModal({ type:"rebuild", array:a })}>
                        🔄 ${t("raid.rebuild")}
                      </button>
                      <button class="action-btn danger-btn" disabled=${busy} onClick=${() => handleDelete(a)}>
                        🗑️ ${t("raid.delete")}
                      </button>
                    </td>
                  </tr>
                `;
              })}
            </tbody>
          </table>
        </div>
      `}

      ${modal?.type === "create" && html`<${CreateArrayModal}
        availableDisks=${availableDisks}
        onClose=${() => setModal(null)}
        onCreated=${() => { setModal(null); load(); }}
        onErr=${setErr}
      />`}
      ${modal?.type === "expand" && html`<${ExpandModal}
        array=${modal.array}
        availableDisks=${availableDisks}
        allDisks=${allDisks}
        onClose=${() => setModal(null)}
        onConfirm=${(devs) => handleExpand(modal.array, devs)}
      />`}
      ${modal?.type === "markFailed" && html`<${MarkFailedModal}
        array=${modal.array}
        onClose=${() => setModal(null)}
        onConfirm=${(d) => handleMarkFailed(modal.array, d)}
      />`}
      ${modal?.type === "rebuild" && html`<${RebuildModal}
        array=${modal.array}
        availableDisks=${availableDisks}
        onClose=${() => setModal(null)}
        onConfirm=${(fd, nd) => handleRebuild(modal.array, fd, nd)}
      />`}
      ${modal?.type === "wipeDisk" && html`<${WipeDiskModal}
        device=${modal.device}
        onClose=${() => setModal(null)}
        onConfirm=${() => handleWipeDisk(modal.device)}
      />`}
      <${FilesystemSection} filesystems=${filesystems} arrays=${enrichedArrays} onLoad=${load} onErr=${setErr} />

      <${SmartPanel} disks=${allDisks} />
    </section>
  `;
}

function WipeDiskModal({ device, onClose, onConfirm }) {
  const [busy, setBusy] = useState(false);
  useEscClose(onClose);
  const go = async () => { setBusy(true); await onConfirm(); setBusy(false); };
  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>🧹 Wipe ${device}</h2><button class="link" disabled=${busy} onClick=${onClose}>✕</button></div>
        <p class="warn-box" style="margin-top:4px">⚠️ <strong>WARNING</strong>: all <strong>content and partitions</strong> on <strong class="mono">${device}</strong> will be <strong>permanently deleted</strong>. This cannot be undone.</p>
        <p class="muted" style="font-size:13px;margin-top:10px">Any leftover RAID configuration on the disk is removed first. Mounted disks or arrays are refused.</p>
        <div class="form-actions">
          <button class="link" disabled=${busy} onClick=${onClose}>Cancel</button>
          <button class="primary" style="background:var(--danger)" disabled=${busy} onClick=${go}>${busy ? "Wiping…" : "🧹 Wipe Disk"}</button>
        </div>
      </div>
    </div>`;
}

function CreateArrayModal({ availableDisks, onClose, onCreated, onErr }) {
  const [selected, setSelected] = useState(new Set());
  const [level, setLevel] = useState("");
  const [name, setName] = useState("md0");
  const [err, setErr] = useState("");

  const toggle = (device) => {
    const next = new Set(selected);
    if (next.has(device)) next.delete(device); else next.add(device);
    setSelected(next);
    setLevel("");
  };

  const count = selected.size;
  const levelOptions = count < 2 ? [] :
    count === 2 ? [["0","RAID 0"],["1","RAID 1"]] :
    count === 3 ? [["0","RAID 0"],["5","RAID 5"]] :
    [["0","RAID 0"],["1","RAID 1"],["5","RAID 5"],["6","RAID 6"],["10","RAID 10"]];

  const selObjs = availableDisks.filter((d) => selected.has(d.device));
  const sizes = selObjs.map((d) => d.size_bytes).filter((s) => s > 0);
  const minSize = sizes.length > 0 ? Math.min(...sizes) : 0;
  const estCapacity = level ? calcRaidCapacity(level, count, minSize) : 0;

  const submit = async () => {
    setErr("");
    if (!level) { setErr(t("raid.selectLevel")); return; }
    if (!confirm(t("raid.confirmCreate"))) return;
    const mdDevice = "/dev/" + name.replace(/^\/dev\//, "");
    try {
      await api.createArray({ device: mdDevice, level, devices: [...selected], confirm: true });
      onCreated();
    } catch (e) { setErr(e.message); onErr(e.message); }
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>${t("raid.create")}</h2><button class="link" onClick=${onClose}>✕</button></div>
        ${err && html`<p class="error">${err}</p>`}
        <p class="section-label">${t("raid.selectDisks")}</p>
        ${availableDisks.length === 0 && html`<p class="muted">${t("raid.noDisksAvailable")}</p>`}
        <div class="disk-list">
          ${availableDisks.map((d) => html`
            <label class="disk-row">
              <input type="checkbox" checked=${selected.has(d.device)} onChange=${() => toggle(d.device)} />
              <span class="mono">${d.device.replace("/dev/","")}</span>
              <span class="muted"> ${fmtBytes(d.size_bytes)} · ${d.rotational?"HDD":"SSD"}${d.model?" · "+d.model:""}</span>
            </label>
          `)}
        </div>
        <div class="form-grid" style="margin-top:16px">
          <label>${t("raid.arrayName")}<input value=${name} placeholder="md0" onInput=${(e) => setName(e.target.value)} /></label>
          <label>${t("raid.level")}
            <select value=${level} onChange=${(e) => setLevel(e.target.value)} disabled=${levelOptions.length===0}>
              <option value="">${count<2 ? t("raid.selectMoreDisks") : t("raid.selectLevelHint")}</option>
              ${levelOptions.map(([v,l]) => html`<option value=${v}>${l}</option>`)}
            </select>
          </label>
        </div>
        ${level && estCapacity > 0 && html`
          <div class="capacity-preview">
            <span>${t("raid.estimatedCapacity")}: <strong class="cap-highlight">${fmtBytes(estCapacity)}</strong></span>
          </div>
        `}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>${t("common.cancel")}</button>
          <button class="primary" disabled=${!level||count<2} onClick=${submit}>${t("raid.add")}</button>
        </div>
      </div>
    </div>
  `;
}

function ExpandModal({ array, availableDisks, allDisks, onClose, onConfirm }) {
  const [selected, setSelected] = useState(new Set());
  const [err, setErr] = useState("");

  const canExpand = !["0","1"].includes(String(array.level));
  const needsPairs = String(array.level) === "10";

  const toggle = (device) => {
    const next = new Set(selected);
    if (next.has(device)) next.delete(device); else next.add(device);
    setSelected(next);
    setErr("");
  };

  const memberObjs = allDisks.filter((d) => array.devices.includes(d.device));
  const curSizes = memberObjs.map((d) => d.size_bytes).filter((s) => s > 0);
  const curMinSize = curSizes.length > 0 ? Math.min(...curSizes) : 0;
  const curCapacity = calcRaidCapacity(array.level, array.devices.length, curMinSize);

  const newObjs = availableDisks.filter((d) => selected.has(d.device));
  const allObjs = [...memberObjs, ...newObjs];
  const allSizes = allObjs.map((d) => d.size_bytes).filter((s) => s > 0);
  const newMinSize = allSizes.length > 0 ? Math.min(...allSizes) : 0;
  const newN = array.devices.length + newObjs.length;
  const newCapacity = calcRaidCapacity(array.level, newN, newMinSize);

  const confirm = () => {
    if (!canExpand) return;
    if (selected.size === 0) { setErr(t("raid.selectAtLeastOne")); return; }
    if (needsPairs && selected.size % 2 !== 0) { setErr(t("raid.expandRaid10Hint")); return; }
    onConfirm([...selected]);
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>🔧 ${t("raid.expandTitle")}</h2><button class="link" onClick=${onClose}>✕</button></div>
        <div class="info-box">
          <span class="mono">${array.device}</span>
          <span class="muted"> · RAID ${array.level} · ${array.devices.length} ${t("raid.disks")} · ${fmtBytes(curCapacity)}</span>
        </div>
        ${!canExpand ? html`
          <p class="warn-box">⚠️ ${t("raid.notExpandable")}</p>
        ` : html`
          <p class="section-label">${t("raid.selectDisks")}</p>
          ${availableDisks.length === 0 && html`<p class="muted">${t("raid.noDisksAvailable")}</p>`}
          <div class="disk-list">
            ${availableDisks.map((d) => html`
              <label class="disk-row">
                <input type="checkbox" checked=${selected.has(d.device)} onChange=${() => toggle(d.device)} />
                <span class="mono">${d.device.replace("/dev/","")}</span>
                <span class="muted"> ${fmtBytes(d.size_bytes)} · ${d.rotational?"HDD":"SSD"}${d.model?" · "+d.model:""}</span>
              </label>
            `)}
          </div>
          ${needsPairs && html`<p class="hint-text">ℹ️ ${t("raid.expandRaid10Hint")}</p>`}
          <div class="capacity-preview">
            <span>${t("raid.currentCapacity")}: <strong>${fmtBytes(curCapacity)}</strong></span>
            ${selected.size > 0 && html`
              <span class="arrow-sep">→</span>
              <span>${t("raid.newCapacity")}: <strong class="cap-highlight">${fmtBytes(newCapacity)}</strong></span>
            `}
          </div>
        `}
        ${err && html`<p class="error">${err}</p>`}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>${t("common.cancel")}</button>
          <button class="primary" disabled=${!canExpand||selected.size===0} onClick=${confirm}>
            ${t("raid.confirmExpand")}
          </button>
        </div>
      </div>
    </div>
  `;
}

function MarkFailedModal({ array, onClose, onConfirm }) {
  const [sel, setSel] = useState("");
  const activeDisks = array.devices.filter((d) => !array.failedDevices.has(d));

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>⚠️ ${t("raid.markFailedTitle")}</h2><button class="link" onClick=${onClose}>✕</button></div>
        <p class="muted" style="margin-bottom:12px">${t("raid.selectFailedDisk")}</p>
        <div class="disk-list">
          ${activeDisks.map((dev) => html`
            <label class="disk-row">
              <input type="radio" name="failedDisk" value=${dev} checked=${sel===dev} onChange=${() => setSel(dev)} />
              <span class="mono">${dev.replace("/dev/","")}</span>
            </label>
          `)}
        </div>
        <div class="form-actions">
          <button class="link" onClick=${onClose}>${t("common.cancel")}</button>
          <button class="primary danger-primary" disabled=${!sel}
            onClick=${() => { if(window.confirm(t("raid.confirmMarkFailed").replace("{disk}", sel))) onConfirm(sel); }}>
            ${t("raid.confirmMarkFailedBtn")}
          </button>
        </div>
      </div>
    </div>
  `;
}

function RebuildModal({ array, availableDisks, onClose, onConfirm }) {
  const [sel, setSel] = useState("");
  const failedDisk = [...array.failedDevices][0] || "";

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>🔄 ${t("raid.rebuildTitle")}</h2><button class="link" onClick=${onClose}>✕</button></div>
        <div class="warn-box" style="margin-bottom:16px">
          ${t("raid.rebuildMsg").replace("{disk}", failedDisk || "?")}
        </div>
        <label class="section-label">${t("raid.selectReplacement")}
          <select value=${sel} onChange=${(e) => setSel(e.target.value)} style="display:block;width:100%;margin-top:6px;padding:8px 10px;background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);font-size:14px">
            <option value="">${t("raid.selectDiskHint")}</option>
            ${availableDisks.map((d) => html`
              <option value=${d.device}>${d.device.replace("/dev/","")}${d.model?" — "+d.model:""} (${fmtBytes(d.size_bytes)})</option>
            `)}
          </select>
        </label>
        <div class="form-actions">
          <button class="link" onClick=${onClose}>${t("common.cancel")}</button>
          <button class="primary" disabled=${!sel} onClick=${() => onConfirm(failedDisk, sel)}>${t("raid.startRebuild")}</button>
        </div>
      </div>
    </div>
  `;
}

function FilesystemSection({ filesystems, arrays, onLoad, onErr }) {
  const [modal, setModal] = useState(null);
  const [growing, setGrowing] = useState(null);
  const [growProgress, setGrowProgress] = useState(0);

  if (filesystems === null) return null;

  const mounted   = filesystems.filter(f => f.is_mounted);
  const hasFS     = filesystems.filter(f => !f.is_mounted && f.fstype);
  const noFS      = filesystems.filter(f => !f.fstype);

  const handleUnmount = async (fs) => {
    if (!window.confirm(`Unmount ${fs.device} from ${fs.mount_point}?`)) return;
    try { await api.unmountFilesystem(fs.device, fs.mount_point); onLoad(); }
    catch (e) { onErr(e.message); }
  };

  const handleGrow = async (fs) => {
    if (!window.confirm(`Expand filesystem on ${fs.device} to fill the full RAID array?`)) return;
    setGrowing(fs.device); setGrowProgress(0);
    let step = 0;
    const timer = setInterval(() => {
      step++;
      setGrowProgress(Math.min(90, Math.round((step / 60) * 100)));
      if (step >= 60) clearInterval(timer);
    }, 100);
    try {
      await api.growFilesystem(fs.device, fs.fstype);
      clearInterval(timer); setGrowProgress(100);
      setTimeout(() => { setGrowing(null); onLoad(); }, 600);
    } catch (e) {
      clearInterval(timer); onErr(e.message); setGrowing(null);
    }
  };

  const handleDeleteFS = async (fs) => {
    const where = fs.is_mounted ? ` mounted at ${fs.mount_point}` : "";
    if (!window.confirm(`Delete the ${fs.fstype||""} filesystem on ${fs.device}${where}?\n\n⚠️ It will be unmounted (if mounted), the data erased (wipefs) and the mount point folder removed (if empty). The RAID array remains.`)) return;
    try { await api.deleteFilesystem(fs.device); onLoad(); }
    catch (e) { onErr(e.message); }
  };

  const usedPct = (fs) => fs.total_bytes > 0 ? Math.round((fs.used_bytes / fs.total_bytes) * 100) : 0;
  const barColor = (pct) => pct > 85 ? "var(--danger)" : pct > 65 ? "var(--warn)" : "var(--ok)";

  return html`
    <div style="margin-top:32px;border-top:1px solid var(--border);padding-top:24px">
      <h2 class="section-title" style="margin-bottom:16px">Filesystem Management</h2>

      ${filesystems.length === 0 && arrays.length === 0 && html`<p class="muted">No RAID arrays found. Create an array first.</p>`}

      ${mounted.length > 0 && html`
        <div class="table-wrap" style="margin-bottom:20px">
          <table class="data">
            <thead><tr>
              <th>Array</th><th>Level</th><th>Type</th><th>Mount Point</th>
              <th style="min-width:200px">Usage</th><th>Free</th><th>Actions</th>
            </tr></thead>
            <tbody>
              ${mounted.map(fs => {
                const pct = usedPct(fs);
                const isGrowing = growing === fs.device;
                return html`
                  <tr>
                    <td class="mono">${fs.device}</td>
                    <td>RAID ${fs.level}</td>
                    <td><span class="badge active">${fs.fstype}</span></td>
                    <td class="mono">${fs.mount_point}</td>
                    <td>
                      <div style="display:flex;align-items:center;gap:8px">
                        <div class="fs-bar"><div class="fs-bar-fill" style="width:${pct}%;background:${barColor(pct)}"></div></div>
                        <span class="muted" style="font-size:12px;white-space:nowrap">
                          ${fmtBytes(fs.used_bytes)} / ${fmtBytes(fs.total_bytes)}
                        </span>
                      </div>
                      ${isGrowing && html`
                        <div class="smart-progress" style="margin-top:6px">
                          <div class="smart-progress-bar"><div class="smart-progress-fill" style="width:${growProgress}%"></div></div>
                        </div>
                      `}
                    </td>
                    <td class="muted">${fmtBytes(fs.free_bytes)}</td>
                    <td class="actions-cell">
                      <button class="action-btn" disabled=${isGrowing} onClick=${() => handleGrow(fs)}>📈 Grow</button>
                      <button class="action-btn danger-btn" disabled=${isGrowing} onClick=${() => handleUnmount(fs)}>⏏️ Unmount</button>
                      <button class="action-btn danger-btn" disabled=${isGrowing} onClick=${() => handleDeleteFS(fs)}>🗑️ Delete FS</button>
                    </td>
                  </tr>
                `;
              })}
            </tbody>
          </table>
        </div>
      `}

      ${hasFS.length > 0 && html`
        <div style="margin-bottom:16px">
          <p class="section-label" style="margin-bottom:8px">⚠️ Formatted but not mounted</p>
          <div style="display:flex;flex-wrap:wrap;gap:8px">
            ${hasFS.map(fs => html`
              <div class="info-box" style="display:inline-flex;align-items:center;gap:12px;margin:0;padding:10px 14px">
                <span class="mono">${fs.device}</span>
                <span class="badge active" style="font-size:11px">${fs.fstype}</span>
                <button class="action-btn" onClick=${() => setModal({ type:"mount", fs })}>📂 Mount</button>
                <button class="action-btn danger-btn" onClick=${() => handleDeleteFS(fs)}>🗑️ Delete FS</button>
              </div>
            `)}
          </div>
        </div>
      `}

      ${noFS.length > 0 && html`
        <div>
          <p class="section-label" style="margin-bottom:8px">Arrays without a filesystem:</p>
          <div style="display:flex;flex-wrap:wrap;gap:8px">
            ${noFS.map(fs => html`
              <div class="info-box" style="display:inline-flex;align-items:center;gap:12px;margin:0;padding:10px 14px">
                <span class="mono">${fs.device}</span>
                <span class="muted">RAID ${fs.level} · ${fs.state}</span>
                <button class="primary" style="font-size:13px;padding:5px 12px"
                  onClick=${() => setModal({ type:"createFS", fs })}>+ Create Filesystem</button>
              </div>
            `)}
          </div>
        </div>
      `}

      ${modal?.type === "createFS" && html`<${CreateFilesystemModal}
        fs=${modal.fs} onClose=${() => setModal(null)}
        onDone=${() => { setModal(null); onLoad(); }} onErr=${onErr} />`}
      ${modal?.type === "mount" && html`<${MountExistingModal}
        fs=${modal.fs} onClose=${() => setModal(null)}
        onDone=${() => { setModal(null); onLoad(); }} onErr=${onErr} />`}
    </div>
  `;
}

function CreateFilesystemModal({ fs, onClose, onDone, onErr }) {
  const defaultMount = "/srv/nas/" + fs.device.replace("/dev/", "");
  const [fsType, setFsType] = useState("ext4");
  const [mountPoint, setMountPoint] = useState(defaultMount);
  const [formatting, setFormatting] = useState(false);
  const [progress, setProgress] = useState(0);
  const [done, setDone] = useState(false);
  const [err, setErr] = useState("");
  const [picker, setPicker] = useState(false);
  useEscClose(onClose);

  const submit = async () => {
    if (!mountPoint.startsWith("/")) { setErr("Mount point must start with / (e.g. /srv/nas/md0)"); return; }
    if (!window.confirm(`Format ${fs.device} as ${fsType} and mount at ${mountPoint}?\n\nWARNING: All data on ${fs.device} will be permanently erased!`)) return;
    setErr(""); setFormatting(true); setProgress(0);
    let step = 0;
    const timer = setInterval(() => {
      step++;
      setProgress(Math.min(90, Math.round((step / 80) * 100)));
      if (step >= 80) clearInterval(timer);
    }, 100);
    try {
      await api.createFilesystem(fs.device, fsType, mountPoint);
      clearInterval(timer); setProgress(100); setDone(true); setFormatting(false);
    } catch (e) {
      clearInterval(timer); setErr(e.message); onErr(e.message); setFormatting(false);
    }
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && !formatting && onClose()}>
      <div class="modal-card">
        <div class="modal-head">
          <h2>💾 Create Filesystem</h2>
          <button class="link" disabled=${formatting} onClick=${onClose}>✕</button>
        </div>
        ${done ? html`
          <div class="success-box">✅ Filesystem created and mounted at <strong class="mono">${mountPoint}</strong></div>
          <div class="form-actions"><button class="primary" onClick=${onDone}>Done</button></div>
        ` : html`
          <div class="info-box"><span class="mono">${fs.device}</span><span class="muted"> · RAID ${fs.level} · ${fs.state}</span></div>
          <p class="warn-box" style="margin-top:12px">⚠️ Formatting will permanently erase all data on <strong>${fs.device}</strong>.</p>
          <div class="form-grid" style="margin-top:16px">
            <label>Filesystem Type
              <select value=${fsType} onChange=${(e) => setFsType(e.target.value)} disabled=${formatting}
                style="display:block;width:100%;margin-top:6px;padding:8px 10px;background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);font-size:14px">
                <option value="ext4">ext4 (recommended)</option>
                <option value="ext3">ext3 (journaled)</option>
                <option value="btrfs">btrfs</option>
                <option value="xfs">XFS (journaled)</option>
              </select>
            </label>
            <label>Mount Point
              <div style="display:flex;gap:8px;margin-top:6px">
                <input value=${mountPoint} disabled=${formatting} placeholder="/srv/nas/md0"
                  onInput=${(e) => { setMountPoint(e.target.value); setErr(""); }}
                  style="flex:1;box-sizing:border-box" />
                <button type="button" class="action-btn" disabled=${formatting} onClick=${() => setPicker(true)}>📁 Browse</button>
              </div>
              <span class="muted" style="font-size:11px">Directory will be created automatically</span>
            </label>
          </div>
          ${picker && html`<${FolderPicker} onClose=${() => setPicker(false)}
            onSelect=${(p) => { setMountPoint(p); setPicker(false); setErr(""); }} />`}
          ${formatting && html`
            <div class="smart-progress" style="margin-top:16px">
              <div class="smart-progress-label"><span>Formatting and mounting…</span><span>${progress}%</span></div>
              <div class="smart-progress-bar"><div class="smart-progress-fill" style="width:${progress}%"></div></div>
            </div>
          `}
          ${err && html`<p class="error" style="margin-top:10px">${err}</p>`}
          <div class="form-actions">
            <button class="link" disabled=${formatting} onClick=${onClose}>Cancel</button>
            <button class="primary" disabled=${formatting} onClick=${submit}>Format & Mount</button>
          </div>
        `}
      </div>
    </div>
  `;
}

function MountExistingModal({ fs, onClose, onDone, onErr }) {
  const defaultMount = "/srv/nas/" + fs.device.replace("/dev/", "");
  const [mountPoint, setMountPoint] = useState(defaultMount);
  const [mounting, setMounting] = useState(false);
  const [err, setErr] = useState("");
  const [picker, setPicker] = useState(false);
  useEscClose(onClose);

  const submit = async () => {
    if (!mountPoint.startsWith("/")) { setErr("Mount point must start with /"); return; }
    setErr(""); setMounting(true);
    try {
      await api.mountFilesystem(fs.device, fs.fstype, mountPoint);
      onDone();
    } catch (e) {
      setErr(e.message); onErr(e.message); setMounting(false);
    }
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && !mounting && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>📂 Mount Filesystem</h2><button class="link" onClick=${onClose}>✕</button></div>
        <div class="info-box"><span class="mono">${fs.device}</span><span class="muted"> · ${fs.fstype}</span></div>
        <div style="margin-top:14px">
          <label>Mount Point
            <div style="display:flex;gap:8px;margin-top:6px">
              <input value=${mountPoint} disabled=${mounting} placeholder="/srv/nas/md0"
                onInput=${(e) => setMountPoint(e.target.value)}
                style="flex:1;box-sizing:border-box" />
              <button type="button" class="action-btn" disabled=${mounting} onClick=${() => setPicker(true)}>📁 Browse</button>
            </div>
          </label>
        </div>
        ${picker && html`<${FolderPicker} onClose=${() => setPicker(false)}
          onSelect=${(p) => { setMountPoint(p); setPicker(false); }} />`}
        ${err && html`<p class="error">${err}</p>`}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" disabled=${mounting} onClick=${submit}>Mount</button>
        </div>
      </div>
    </div>
  `;
}

function SmartPanel({ disks }) {
  const [sel, setSel] = useState("");
  const [checking, setChecking] = useState(false);
  const [progress, setProgress] = useState(0);
  const [checkType, setCheckType] = useState("");
  const [result, setResult] = useState(null);
  const [err, setErr] = useState("");

  const runCheck = (type) => {
    if (!sel) return;
    setErr(""); setResult(null); setProgress(0); setChecking(true); setCheckType(type);
    const steps = type === "short" ? 40 : 100;
    let step = 0;
    const timer = setInterval(() => {
      step++;
      setProgress(Math.min(99, Math.round((step / steps) * 100)));
      if (step >= steps) {
        clearInterval(timer);
        api.smart(sel.replace(/^\/dev\//, ""))
          .then((info) => { setResult({ ...info, checkType: type }); setProgress(100); setChecking(false); })
          .catch((e) => { setErr(e.message); setChecking(false); });
      }
    }, 100);
  };

  const ok = result && result.smart_health === "PASSED";

  return html`
    <div class="smart-panel">
      <h2>${t("raid.smartTitle")}</h2>
      <div class="smart-controls">
        <select value=${sel} onChange=${(e) => { setSel(e.target.value); setResult(null); }}
          disabled=${checking} class="smart-select">
          <option value="">${t("raid.selectDiskHint")}</option>
          ${disks.map((d) => html`
            <option value=${d.device}>${d.device.replace("/dev/","")}${d.model?" — "+d.model:""} (${fmtBytes(d.size_bytes)})</option>
          `)}
        </select>
        <button class="primary" disabled=${!sel||checking} onClick=${() => runCheck("short")}>${t("raid.shortCheck")}</button>
        <button class="btn-secondary" disabled=${!sel||checking} onClick=${() => runCheck("extended")}>${t("raid.extendedCheck")}</button>
      </div>

      ${checking && html`
        <div class="smart-progress">
          <div class="smart-progress-label">
            <span>${t(checkType==="short" ? "raid.shortRunning" : "raid.extendedRunning")}</span>
            <span>${progress}%</span>
          </div>
          <div class="smart-progress-bar"><div class="smart-progress-fill" style="width:${progress}%"></div></div>
        </div>
      `}

      ${err && html`<p class="error">${err}</p>`}

      ${result && html`
        <div class="smart-result-card ${ok?"ok":"fail"}">
          <div class="smart-result-header">
            <span class="smart-icon">${ok?"✅":"⚠️"}</span>
            <strong>${result.device}</strong>
            <span class="badge ${ok?"active":"degraded"}">${result.smart_health}</span>
            <span class="muted smart-type">${t(result.checkType==="short"?"raid.shortCheck":"raid.extendedCheck")}</span>
          </div>
          <table class="data smart-result">
            <tbody>
              <tr><th>${t("raid.smartModel")}</th><td>${result.model||"—"}</td></tr>
              <tr><th>${t("raid.smartTemp")}</th><td>${result.temperature ? result.temperature+" °C" : "—"}</td></tr>
              <tr><th>${t("raid.powerOnHours")}</th><td>${result.power_on_hours ? result.power_on_hours+" h" : "—"}</td></tr>
              <tr><th>${t("raid.reallocatedSectors")}</th><td>${result.reallocated_sectors!=null ? result.reallocated_sectors : "—"}</td></tr>
              ${result.checkType==="extended" && html`
                <tr><th>${t("raid.smartHealth")}</th><td>${result.smart_health}</td></tr>
                <tr><th>${t("raid.readSpeed")}</th><td>N/A</td></tr>
                <tr><th>${t("raid.testHistory")}</th><td>${t("raid.testHistoryNA")}</td></tr>
              `}
            </tbody>
          </table>
        </div>
      `}
    </div>
  `;
}

const US_SHARES = ["media", "backup", "documents", "public"];

function mapAPIUser(u) {
  return {
    id: u.id,
    username: u.username,
    fullName: "",
    email: "",
    group: u.is_admin ? "admins" : "users",
    role: u.is_admin ? "Admin" : "User",
    status: "Active",
    lastLogin: "—",
    created: u.created_at ? u.created_at.slice(0, 10) : "—",
    modified: "—",
    additionalGroups: [],
    accountExpiry: "",
    enabled: true,
    sharePerms: {},
  };
}

function UsersView({ isAdmin }) {
  const [users, setUsers] = useState([]);
  const [groups] = useState([
    { id:1, name:"admins",   description:"Administrators",   type:"System", members:[], sharePerms:{media:"rw",backup:"rw",documents:"rw",public:"rw"} },
    { id:2, name:"users",    description:"Standard Users",   type:"Custom", members:[], sharePerms:{media:"ro",backup:"none",documents:"rw",public:"rw"} },
    { id:3, name:"media",    description:"Media Consumers",  type:"Custom", members:[], sharePerms:{media:"ro",backup:"none",documents:"none",public:"ro"} },
    { id:4, name:"services", description:"Service Accounts", type:"System", members:[], sharePerms:{media:"none",backup:"rw",documents:"none",public:"none"} },
    { id:5, name:"guests",   description:"Guest Access",     type:"System", members:[], sharePerms:{media:"none",backup:"none",documents:"none",public:"ro"} },
  ]);
  const [panel, setPanel] = useState("users");
  const [modal, setModal] = useState(null);
  const [newRowId, setNewRowId] = useState(null);
  const [apiErr, setApiErr] = useState("");

  const [shares, setShares] = useState([]);

  const loadUsers = useCallback(() => {
    api.listUsers().then(list => setUsers((list || []).map(mapAPIUser))).catch(() => {});
    api.listShares().then(list => setShares(list || [])).catch(() => {});
  }, []);

  useEffect(() => { loadUsers(); }, [loadUsers]);

  const activeCount = users.filter(u => u.status === "Active").length;
  const lockedCount = users.filter(u => u.status !== "Active").length;
  const adminCount  = users.filter(u => u.role === "Admin").length;

  const addUser = (data) => {
    setApiErr("");
    api.createUser({
      username: data.username,
      comment: data.fullName,
      password: data.password,
      is_admin: data.role === "Admin",
    }).then(() => {
      loadUsers();
      setNewRowId(data.username);
      setTimeout(() => setNewRowId(null), 1200);
      setModal(null);
    }).catch(e => setApiErr(e.message));
  };

  const editUser = (data) => {
    setApiErr("");
    const doEdit = () => {
      const today = new Date().toISOString().slice(0, 10);
      setUsers(prev => prev.map(u => u.id === data.id ? { ...u, ...data, modified: today } : u));
      setModal(null);
    };
    if (data.password) {
      api.setPassword(data.username, data.password).then(doEdit).catch(e => setApiErr(e.message));
    } else {
      doEdit();
    }
  };

  const deleteUser = (id) => {
    const user = users.find(u => u.id === id);
    if (!user) return;
    setApiErr("");
    api.deleteUser(user.username).then(() => {
      loadUsers();
      setModal(null);
    }).catch(e => setApiErr(e.message));
  };

  const toggleLock = (id) => {
    setUsers(prev => prev.map(u => {
      if (u.id !== id) return u;
      return { ...u, status: u.status === "Active" ? "Locked" : "Active" };
    }));
  };

  const addGroup = (data) => {
    const id = Math.max(0, ...groups.map(g => g.id)) + 1;
    setModal(null);
  };

  const editGroup = (data) => { setModal(null); };
  const deleteGroup = (id) => { setModal(null); };
  const saveMembers = (gid, members) => { setModal(null); };

  return html`
    <section class="users-view">
      <header class="page-head"><h1>User Management</h1></header>
      ${apiErr && html`<div class="error-box" style="margin-bottom:12px">${apiErr}</div>`}

      <div class="stats-row">
        <div class="stat-card">
          <span class="stat-icon">👤</span>
          <div><span class="stat-val">${users.length}</span><span class="stat-label">Total Users</span></div>
        </div>
        <div class="stat-card">
          <span class="stat-icon">✅</span>
          <div><span class="stat-val ok">${activeCount}</span><span class="stat-label">Active</span></div>
        </div>
        <div class="stat-card">
          <span class="stat-icon">🔒</span>
          <div><span class="stat-val ${lockedCount > 0 ? "warn" : ""}">${lockedCount}</span><span class="stat-label">Locked / Disabled</span></div>
        </div>
        <div class="stat-card">
          <span class="stat-icon">👥</span>
          <div><span class="stat-val">${groups.length}</span><span class="stat-label">Groups</span></div>
        </div>
      </div>

      <div class="panel-tabs">
        <button class=${"panel-tab" + (panel === "users" ? " active" : "")} onClick=${() => setPanel("users")}>👤 Users</button>
        <button class=${"panel-tab" + (panel === "groups" ? " active" : "")} onClick=${() => setPanel("groups")}>👥 Groups</button>
      </div>

      ${panel === "users" ? html`<${UsersPanel}
        users=${users} groups=${groups} adminCount=${adminCount} newRowId=${newRowId}
        isAdmin=${isAdmin}
        onAdd=${() => setModal({ type:"addUser" })}
        onEdit=${(u) => setModal({ type:"editUser", user:u })}
        onDelete=${(u) => setModal({ type:"deleteUser", user:u })}
        onToggleLock=${toggleLock}
      />` : html`<${GroupsPanel}
        groups=${groups} users=${users}
        isAdmin=${isAdmin}
        onAdd=${() => setModal({ type:"addGroup" })}
        onEdit=${(g) => setModal({ type:"editGroup", group:g })}
        onManageMembers=${(g) => setModal({ type:"manageMembers", group:g })}
        onDelete=${(g) => setModal({ type:"deleteGroup", group:g })}
      />`}

      ${isAdmin && modal?.type === "addUser" && html`<${AddEditUserModal}
        groups=${groups} users=${users} shares=${shares} onClose=${() => setModal(null)} onSave=${addUser} />`}
      ${isAdmin && modal?.type === "editUser" && html`<${AddEditUserModal}
        user=${modal.user} groups=${groups} users=${users} shares=${shares} onClose=${() => setModal(null)} onSave=${editUser} />`}
      ${isAdmin && modal?.type === "deleteUser" && html`<${DeleteUserModal}
        user=${modal.user} adminCount=${adminCount} onClose=${() => setModal(null)} onConfirm=${() => deleteUser(modal.user.id)} />`}
      ${isAdmin && modal?.type === "addGroup" && html`<${AddEditGroupModal}
        users=${users} groups=${groups} onClose=${() => setModal(null)} onSave=${addGroup} />`}
      ${isAdmin && modal?.type === "editGroup" && html`<${AddEditGroupModal}
        group=${modal.group} users=${users} groups=${groups} onClose=${() => setModal(null)} onSave=${editGroup} />`}
      ${isAdmin && modal?.type === "manageMembers" && html`<${ManageMembersModal}
        group=${modal.group} users=${users} onClose=${() => setModal(null)} onSave=${(m) => saveMembers(modal.group.id, m)} />`}
      ${isAdmin && modal?.type === "deleteGroup" && html`<${DeleteGroupModal}
        group=${modal.group} users=${users} onClose=${() => setModal(null)} onConfirm=${() => deleteGroup(modal.group.id)} />`}
    </section>
  `;
}

function RoleBadge({ role }) {
  const cls = { Admin:"role-admin", User:"role-user", Service:"role-service", Guest:"role-guest" }[role] || "role-guest";
  const icon = { Admin:"🔴", User:"🔵", Service:"🟡", Guest:"⚫" }[role] || "⚫";
  return html`<span class=${"role-badge " + cls}>${icon} ${role}</span>`;
}

function StatusBadge({ status }) {
  const cls = { Active:"status-active", Disabled:"status-disabled", Locked:"status-locked" }[status] || "status-disabled";
  return html`<span class=${"status-badge " + cls}>${status}</span>`;
}

function UsersPanel({ users, groups, adminCount, newRowId, isAdmin, onAdd, onEdit, onDelete, onToggleLock }) {
  const [search, setSearch] = useState("");
  const [filterGroup, setFilterGroup] = useState("");
  const [filterRole, setFilterRole] = useState("");

  const filtered = users.filter(u => {
    const s = search.toLowerCase();
    const ok = !s || u.username.includes(s) || u.fullName.toLowerCase().includes(s);
    return ok && (!filterGroup || u.group === filterGroup) && (!filterRole || u.role === filterRole);
  });

  return html`
    <div>
      <div class="us-toolbar">
        <div class="us-search-wrap">
          <span class="search-icon">🔍</span>
          <input class="us-search" placeholder="Search by username or name…" value=${search}
            onInput=${(e) => setSearch(e.target.value)} />
        </div>
        <select class="us-filter" value=${filterGroup} onChange=${(e) => setFilterGroup(e.target.value)}>
          <option value="">All Groups</option>
          ${[...new Set(groups.map(g => g.name))].map(n => html`<option value=${n}>${n}</option>`)}
        </select>
        <select class="us-filter" value=${filterRole} onChange=${(e) => setFilterRole(e.target.value)}>
          <option value="">All Roles</option>
          ${["Admin","User","Service","Guest"].map(r => html`<option value=${r}>${r}</option>`)}
        </select>
        ${isAdmin && html`<button class="primary" onClick=${onAdd}>+ Add User</button>`}
      </div>
      <div class="table-wrap">
        <table class="data us-table">
          <thead><tr>
            <th>Username</th><th>Full Name</th><th>Email</th><th>Group</th>
            <th>Role</th><th>Status</th><th>Last Login</th>
            ${isAdmin && html`<th>Actions</th>`}
          </tr></thead>
          <tbody>
            ${filtered.map(u => html`
              <tr class=${"us-row" + (u.username === newRowId ? " row-fade-in" : "")}>
                <td class="mono">${u.username}</td>
                <td>${u.fullName}</td>
                <td class="muted">${u.email || "—"}</td>
                <td><span class="group-tag">${u.group}</span></td>
                <td><${RoleBadge} role=${u.role} /></td>
                <td><${StatusBadge} status=${u.status} /></td>
                <td class="muted">${u.lastLogin}</td>
                ${isAdmin && html`<td class="us-actions">
                  <button class="icon-btn" title="Edit" onClick=${() => onEdit(u)}>✏️</button>
                  ${u.username === "admin"
                    ? html`<span class="icon-btn disabled" title="Admin account cannot be locked">🔒</span>`
                    : html`<button class="icon-btn" title=${u.status === "Active" ? "Lock" : "Unlock"}
                        onClick=${() => onToggleLock(u.id)}>${u.status === "Active" ? "🔒" : "🔓"}</button>`}
                  <button class="icon-btn danger-icon" title="Delete" onClick=${() => onDelete(u)}>🗑️</button>
                </td>`}
              </tr>
            `)}
            ${filtered.length === 0 && html`
              <tr><td colspan=${isAdmin ? 8 : 7} class="muted" style="text-align:center;padding:24px">No users found</td></tr>`}
          </tbody>
        </table>
      </div>
    </div>
  `;
}

function GroupsPanel({ groups, users, isAdmin, onAdd, onEdit, onManageMembers, onDelete }) {
  const [search, setSearch] = useState("");

  const getPermLabel = (perms) => {
    const vals = Object.values(perms || {});
    if (vals.every(v => v === "rw")) return "Full Access";
    if (vals.every(v => v === "none")) return "Minimal";
    if (vals.every(v => v !== "rw")) return "Read-Only";
    return "Custom";
  };

  const filtered = groups.filter(g => {
    const s = search.toLowerCase();
    return !s || g.name.includes(s) || g.description.toLowerCase().includes(s);
  });

  return html`
    <div>
      <div class="us-toolbar">
        <div class="us-search-wrap">
          <span class="search-icon">🔍</span>
          <input class="us-search" placeholder="Search groups…" value=${search}
            onInput=${(e) => setSearch(e.target.value)} />
        </div>
        ${isAdmin && html`<button class="primary" onClick=${onAdd}>+ Add Group</button>`}
      </div>
      <div class="table-wrap">
        <table class="data us-table">
          <thead><tr>
            <th>Group Name</th><th>Description</th><th>Type</th>
            <th>Members</th><th>Permissions</th>
            ${isAdmin && html`<th>Actions</th>`}
          </tr></thead>
          <tbody>
            ${filtered.map(g => html`
              <tr class="us-row">
                <td class="mono">${g.name}</td>
                <td>${g.description}</td>
                <td><span class=${"type-badge " + (g.type === "System" ? "type-system" : "type-custom")}>${g.type}</span></td>
                <td>${g.members.length} member${g.members.length !== 1 ? "s" : ""}</td>
                <td class="muted">${getPermLabel(g.sharePerms)}</td>
                ${isAdmin && html`<td class="us-actions">
                  <button class="icon-btn" title="Edit Group" onClick=${() => onEdit(g)}>✏️</button>
                  <button class="icon-btn" title="Manage Members" onClick=${() => onManageMembers(g)}>👥</button>
                  ${g.type === "System"
                    ? html`<span class="icon-btn disabled" title="System groups cannot be deleted">🗑️</span>`
                    : html`<button class="icon-btn danger-icon" title="Delete Group" onClick=${() => onDelete(g)}>🗑️</button>`}
                </td>`}
              </tr>
            `)}
          </tbody>
        </table>
      </div>
    </div>
  `;
}

function useEscClose(onClose) {
  useEffect(() => {
    const h = (e) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onClose]);
}

function PermsTable({ shares, sharePerms, onChange, disabled }) {
  if (!shares || shares.length === 0)
    return html`<p class="muted" style="padding:8px 2px">No shares configured. Create one in the Shares section.</p>`;
  return html`
    <table class="perms-table">
      <thead><tr><th>Share</th><th>Permission</th></tr></thead>
      <tbody>
        ${shares.map(s => html`
          <tr>
            <td><span class="mono">${s.name}</span> <span class="muted" style="font-size:11px">${s.protocol === "smb" ? "CIFS" : "NFS"} · ${s.path}</span></td>
            <td>
              <select value=${sharePerms[s.id] || "none"} disabled=${disabled}
                onChange=${(e) => onChange(s.id, e.target.value)}>
                <option value="none">No Access</option>
                <option value="ro">Read Only</option>
                <option value="rw">Read-Write</option>
              </select>
            </td>
          </tr>
        `)}
      </tbody>
    </table>
  `;
}

function AddEditUserModal({ user, groups, users, shares, onClose, onSave }) {
  const isEdit = !!user;
  const [form, setForm] = useState(() => isEdit ? {
    id: user.id, username: user.username, fullName: user.fullName || "",
    email: user.email || "", password: "", confirmPassword: "",
    role: user.role, group: user.group,
    additionalGroups: [...(user.additionalGroups || [])],
    accountExpiry: user.accountExpiry || "", enabled: user.enabled !== false,
    sharePerms: { ...(user.sharePerms || {}) },
  } : {
    username: "", fullName: "", email: "", password: "", confirmPassword: "",
    role: "User", group: groups[0]?.name || "",
    additionalGroups: [], accountExpiry: "", enabled: true,
    sharePerms: {},
  });
  const [errors, setErrors] = useState({});
  const [showPass, setShowPass] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [showPerms, setShowPerms] = useState(false);

  useEscClose(onClose);

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }));
  const setPerms = (s, v) => setForm(f => ({ ...f, sharePerms: { ...f.sharePerms, [s]: v } }));
  const clearErr = (k) => setErrors(e => ({ ...e, [k]: "" }));

  const toggleGroup = (name) => setForm(f => ({
    ...f, additionalGroups: f.additionalGroups.includes(name)
      ? f.additionalGroups.filter(g => g !== name)
      : [...f.additionalGroups, name],
  }));

  const validate = () => {
    const e = {};
    if (!isEdit) {
      if (!form.username) e.username = "Username is required";
      else if (!/^[a-z0-9_]+$/.test(form.username)) e.username = "Only lowercase letters, numbers and underscore";
      else if (users.some(u => u.username === form.username)) e.username = "Username already exists";
      if (!form.password) e.password = "Password is required";
    }
    if (form.password && form.password.length < 8) e.password = "Minimum 8 characters";
    if (form.password && form.password !== form.confirmPassword) e.confirmPassword = "Passwords do not match";
    return e;
  };

  const handleSave = () => {
    const e = validate();
    setErrors(e);
    if (Object.keys(e).length > 0) return;
    onSave({ ...form });
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card modal-lg">
        <div class="modal-head">
          <h2>${isEdit ? "✏️ Edit User" : "➕ Add User"}</h2>
          <button class="link" onClick=${onClose}>✕</button>
        </div>

        <p class="modal-section-label">Identity</p>
        <div class="form-grid-3">
          <label>Username ${!isEdit && html`<span class="required">*</span>`}
            <input value=${form.username} disabled=${isEdit}
              class=${errors.username ? "input-error" : ""}
              placeholder="e.g. john_doe"
              onInput=${(e) => { set("username", e.target.value); clearErr("username"); }} />
            ${errors.username && html`<span class="field-error">${errors.username}</span>`}
          </label>
          <label>Full Name
            <input value=${form.fullName} placeholder="John Doe"
              onInput=${(e) => set("fullName", e.target.value)} />
          </label>
          <label>Email
            <input type="email" value=${form.email} placeholder="user@nas.local"
              onInput=${(e) => set("email", e.target.value)} />
          </label>
        </div>

        <p class="modal-section-label" style="margin-top:20px">Access</p>
        <div class="form-grid-3">
          <label>Password ${!isEdit && html`<span class="required">*</span>`}
            <div class="pass-wrap">
              <input type=${showPass ? "text" : "password"} value=${form.password}
                class=${errors.password ? "input-error" : ""}
                placeholder=${isEdit ? "Leave blank to keep current" : "Min. 8 characters"}
                onInput=${(e) => { set("password", e.target.value); clearErr("password"); }} />
              <button class="pass-toggle" type="button" onClick=${() => setShowPass(!showPass)}>${showPass ? "🙈" : "👁️"}</button>
            </div>
            ${errors.password && html`<span class="field-error">${errors.password}</span>`}
          </label>
          <label>Confirm Password
            <div class="pass-wrap">
              <input type=${showConfirm ? "text" : "password"} value=${form.confirmPassword}
                class=${errors.confirmPassword ? "input-error" : ""}
                placeholder="Repeat password"
                onInput=${(e) => { set("confirmPassword", e.target.value); clearErr("confirmPassword"); }} />
              <button class="pass-toggle" type="button" onClick=${() => setShowConfirm(!showConfirm)}>${showConfirm ? "🙈" : "👁️"}</button>
            </div>
            ${errors.confirmPassword && html`<span class="field-error">${errors.confirmPassword}</span>`}
          </label>
          <label>Role
            <select value=${form.role} onChange=${(e) => set("role", e.target.value)}>
              ${["Admin","User","Service","Guest"].map(r => html`<option value=${r}>${r}</option>`)}
            </select>
          </label>
          <label>Primary Group
            <select value=${form.group} onChange=${(e) => set("group", e.target.value)}>
              ${groups.map(g => html`<option value=${g.name}>${g.name}</option>`)}
            </select>
          </label>
          <label>Account Expiry
            <input type="date" value=${form.accountExpiry} onInput=${(e) => set("accountExpiry", e.target.value)} />
          </label>
          <label class="check" style="align-self:end;padding-bottom:6px">
            <input type="checkbox" checked=${form.enabled} onChange=${(e) => set("enabled", e.target.checked)} />
            Account Enabled
          </label>
        </div>

        <p class="modal-section-label" style="margin-top:20px">Additional Groups</p>
        <div class="addl-groups">
          ${groups.filter(g => g.name !== form.group).map(g => html`
            <label class="disk-row">
              <input type="checkbox" checked=${form.additionalGroups.includes(g.name)} onChange=${() => toggleGroup(g.name)} />
              <span class="mono">${g.name}</span>
              <span class="muted"> — ${g.description}</span>
            </label>
          `)}
        </div>

        <button class="collapse-toggle" onClick=${() => setShowPerms(!showPerms)}>
          ${showPerms ? "▾" : "▸"} Folder Permissions (optional)
        </button>
        ${showPerms && html`<${PermsTable} shares=${shares} sharePerms=${form.sharePerms} onChange=${setPerms} />`}

        ${isEdit && html`
          <p class="muted" style="font-size:12px;margin-top:16px;border-top:1px solid var(--border);padding-top:10px">
            Created: ${user.created} — Last modified: ${user.modified}
          </p>
        `}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" onClick=${handleSave}>${isEdit ? "Save Changes" : "Create User"}</button>
        </div>
      </div>
    </div>
  `;
}

function DeleteUserModal({ user, adminCount, onClose, onConfirm }) {
  const isOnlyAdmin = user.role === "Admin" && adminCount <= 1;
  useEscClose(onClose);
  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>🗑️ Delete User</h2><button class="link" onClick=${onClose}>✕</button></div>
        <p>Are you sure you want to delete user <strong class="mono">${user.username}</strong>? This action is irreversible.</p>
        ${user.role === "Admin" && html`<div class="warn-box" style="margin-top:12px">⚠️ Warning: you are deleting an Administrator account.</div>`}
        ${isOnlyAdmin && html`<div class="error-box" style="margin-top:12px">❌ Cannot delete the only administrator in the system.</div>`}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary danger-primary" disabled=${isOnlyAdmin} onClick=${onConfirm}>Delete</button>
        </div>
      </div>
    </div>
  `;
}

function AddEditGroupModal({ group, users, groups, onClose, onSave }) {
  const isEdit = !!group;
  const [form, setForm] = useState(() => isEdit ? {
    id: group.id, name: group.name, description: group.description || "",
    type: group.type, members: [...(group.members || [])],
    sharePerms: { media:"none", backup:"none", documents:"none", public:"none", ...(group.sharePerms || {}) },
  } : {
    name: "", description: "", type: "Custom", members: [],
    sharePerms: { media:"none", backup:"none", documents:"none", public:"none" },
  });
  const [errors, setErrors] = useState({});
  useEscClose(onClose);

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }));
  const setPerms = (s, v) => setForm(f => ({ ...f, sharePerms: { ...f.sharePerms, [s]: v } }));
  const toggleMember = (u) => setForm(f => ({
    ...f, members: f.members.includes(u) ? f.members.filter(m => m !== u) : [...f.members, u],
  }));

  const handleSave = () => {
    const e = {};
    if (!form.name) e.name = "Group name is required";
    else if (!/^[a-z0-9_]+$/.test(form.name)) e.name = "Only lowercase letters, numbers and underscore";
    else if (!isEdit && groups.some(g => g.name === form.name)) e.name = "Group name already exists";
    setErrors(e);
    if (Object.keys(e).length > 0) return;
    onSave({ ...form });
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card modal-lg">
        <div class="modal-head">
          <h2>${isEdit ? "✏️ Edit Group" : "➕ Add Group"}</h2>
          <button class="link" onClick=${onClose}>✕</button>
        </div>
        <div class="form-grid">
          <label>Group Name ${!isEdit && html`<span class="required">*</span>`}
            <input value=${form.name} disabled=${isEdit}
              class=${errors.name ? "input-error" : ""}
              placeholder="e.g. media_users"
              onInput=${(e) => { set("name", e.target.value); setErrors(er => ({...er,name:""})); }} />
            ${errors.name && html`<span class="field-error">${errors.name}</span>`}
          </label>
          <label>Description
            <input value=${form.description} placeholder="Group description"
              onInput=${(e) => set("description", e.target.value)} />
          </label>
          <label>Type<input value=${form.type} disabled /></label>
        </div>

        <p class="modal-section-label" style="margin-top:16px">Members</p>
        <div class="addl-groups">
          ${users.map(u => html`
            <label class="disk-row">
              <input type="checkbox" checked=${form.members.includes(u.username)} onChange=${() => toggleMember(u.username)} />
              <span class="mono">${u.username}</span>
              <span class="muted"> — ${u.fullName}</span>
            </label>
          `)}
        </div>

        <p class="modal-section-label" style="margin-top:16px">Share Permissions</p>
        <${PermsTable} sharePerms=${form.sharePerms} onChange=${setPerms} />

        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" onClick=${handleSave}>${isEdit ? "Save Changes" : "Create Group"}</button>
        </div>
      </div>
    </div>
  `;
}

function ManageMembersModal({ group, users, onClose, onSave }) {
  const [inGroup, setInGroup] = useState(() => [...(group.members || [])]);
  const available = users.filter(u => !inGroup.includes(u.username));
  const inGroupUsers = users.filter(u => inGroup.includes(u.username));
  useEscClose(onClose);

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head">
          <h2>👥 Manage Members — <span class="mono">${group.name}</span></h2>
          <button class="link" onClick=${onClose}>✕</button>
        </div>
        <div class="members-layout">
          <div>
            <p class="modal-section-label">Members (${inGroup.length})</p>
            <div class="member-list">
              ${inGroupUsers.map(u => html`
                <div class="member-item">
                  <span class="mono">${u.username}</span>
                  <button class="icon-btn" title="Remove from group"
                    onClick=${() => setInGroup(p => p.filter(m => m !== u.username))}>→</button>
                </div>
              `)}
              ${inGroupUsers.length === 0 && html`<p class="muted" style="padding:10px;font-size:12px">No members</p>`}
            </div>
          </div>
          <div class="members-divider">⇌</div>
          <div>
            <p class="modal-section-label">Available (${available.length})</p>
            <div class="member-list">
              ${available.map(u => html`
                <div class="member-item">
                  <button class="icon-btn" title="Add to group"
                    onClick=${() => setInGroup(p => [...p, u.username])}>←</button>
                  <span class="mono">${u.username}</span>
                </div>
              `)}
              ${available.length === 0 && html`<p class="muted" style="padding:10px;font-size:12px">All users added</p>`}
            </div>
          </div>
        </div>
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" onClick=${() => onSave(inGroup)}>Save</button>
        </div>
      </div>
    </div>
  `;
}

function DeleteGroupModal({ group, users, onClose, onConfirm }) {
  const affected = users.filter(u => u.group === group.name || (u.additionalGroups || []).includes(group.name));
  useEscClose(onClose);
  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>🗑️ Delete Group</h2><button class="link" onClick=${onClose}>✕</button></div>
        ${group.type === "System" ? html`
          <div class="error-box">❌ System groups cannot be deleted.</div>
        ` : html`
          <p>Deleting <strong class="mono">${group.name}</strong> will not delete its members, but they will lose the associated permissions.</p>
          ${affected.length > 0 && html`
            <div class="warn-box" style="margin-top:12px">
              <p style="margin:0 0 8px">⚠️ Affected users (${affected.length}):</p>
              <div style="display:flex;flex-wrap:wrap;gap:6px">
                ${affected.map(u => html`<span class="disk-tag disk-ok">${u.username}</span>`)}
              </div>
            </div>
          `}
        `}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary danger-primary" disabled=${group.type === "System"} onClick=${onConfirm}>Delete Group</button>
        </div>
      </div>
    </div>
  `;
}

// ---- Helper di formattazione ----
function fmtBytes(n) {
  if (n === 0 || n == null) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(n) / Math.log(1024));
  return `${(n / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${u[i]}`;
}
function fmtDate(s) {
  try { return new Date(s).toLocaleString(); } catch (_) { return s; }
}
function joinPath(base, name) {
  return (base.endsWith("/") ? base : base + "/") + name;
}
function parentPath(p) {
  const i = p.replace(/\/+$/, "").lastIndexOf("/");
  return i <= 0 ? "/" : p.slice(0, i);
}

// ===== SHARES =====
// ===== SHARES =====

function fmtSize(gb) {
  if (gb >= 1000) return (gb/1000).toFixed(1)+"TB";
  if (gb < 1) return Math.round(gb*1000)+"MB";
  return gb+"GB";
}

function mapShareFromAPI(dto, ulist) {
  const vol = dto.path.match(/^\/mnt\/([^/]+)/)?.[1] || dto.path.split("/")[2] || "";
  const defaultAdv = { sync:true, noRootSquash:false, rootSquash:true, allSquash:false, anonuid:65534, anongid:65534, noSubtreeCheck:true, secure:true, crossmnt:false };
  if (dto.protocol === "smb") {
    const vu = (dto.valid_users||[]).filter(u=>!u.startsWith("@")).map(u=>{
      const found = ulist.find(x=>x.username===u);
      return { username:u, role:found?.role||"User", perm:dto.read_only?"ro":"rw" };
    });
    const vg = (dto.valid_users||[]).filter(u=>u.startsWith("@")).map(u=>({ name:u.slice(1), perm:dto.read_only?"ro":"rw" }));
    return {
      id:dto.id, name:dto.name, description:"", volume:vol, path:dto.path,
      createFolder:true, recycleBin:false, hidden:false,
      quota:{ enabled:false, size:200, unit:"GB", warningPct:80 },
      smb:{ networkName:dto.name, minProtocol:"SMB2", signing:false, encryption:false, timeMachine:false, guest:false, oplocks:"enabled", hideDotFiles:false, caseSensitivity:"insensitive", comment:"", logAccess:false },
      users:vu, groups:vg, defaultPerm:dto.read_only?"ro":"rw",
      status:dto.enabled?"active":"disabled", usedGB:0, connections:0, createdAt:"", modifiedAt:"",
    };
  } else {
    const rules = (dto.allowed_ips||[]).length>0
      ? dto.allowed_ips.map((ip,i)=>({ id:i+1, client:ip, perm:dto.read_only?"ro":"rw", advOpen:false, adv:{...defaultAdv} }))
      : [{ id:1, client:"*", perm:dto.read_only?"ro":"rw", advOpen:false, adv:{...defaultAdv} }];
    return {
      id:dto.id, name:dto.name, description:"", volume:vol, path:dto.path, createFolder:true, logAccess:false,
      versions:{ v3:true, v4:true, v41:false, v42:false },
      nfsv4:{ domain:"nas.local", kerberos:false, kdc:"", realm:"", kerberosMode:"krb5" },
      rules, global:{ fsid:false, fsidNum:0, acl:false, pnfs:false, sec:"sys", description:"" },
      status:dto.enabled?"active":"disabled", createdAt:"", modifiedAt:"",
    };
  }
}

function mapCIFSToAPI(form) {
  return {
    name:form.name, path:form.path, protocol:"smb",
    read_only: form.defaultPerm==="ro",
    allowed_ips: [],
    // guest = share pubblica: nessun valid users (il backend emette "guest ok").
    valid_users: form.smb?.guest ? [] : [
      ...form.users.filter(u=>u.perm!=="none").map(u=>u.username),
      ...form.groups.filter(g=>g.perm!=="none").map(g=>"@"+g.name),
    ],
    enabled: true,
  };
}

function mapNFSToAPI(form) {
  return {
    name:form.name, path:form.path, protocol:"nfs",
    read_only: (form.rules||[]).length>0 && form.rules.every(r=>r.perm==="ro"),
    allowed_ips: (form.rules||[]).map(r=>r.client).filter(Boolean),
    valid_users: [],
    enabled: true,
  };
}

function useToast() {
  const [toasts, setToasts] = useState([]);
  const show = useCallback((msg, type = "ok") => {
    const id = Date.now() + Math.random();
    setToasts(prev => [...prev, { id, msg, type }]);
    setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 3500);
  }, []);
  return [toasts, show];
}

function ToastList({ toasts }) {
  if (!toasts.length) return null;
  return html`<div class="toast-wrap">${toasts.map(t => html`<div class=${"toast " + t.type} key=${t.id}>${t.type==="ok"?"✅":"❌"} ${t.msg}</div>`)}</div>`;
}

function Tog({ checked, onChange, disabled }) {
  return html`<label class="tog"><input type="checkbox" checked=${checked} disabled=${disabled||false} onChange=${onChange} /><span class="tog-slider"></span></label>`;
}

function TogRow({ label, checked, onChange, disabled, hint, warn }) {
  return html`
    <div class="tog-wrap" style="margin-bottom:8px">
      <${Tog} checked=${checked} onChange=${onChange} disabled=${disabled} />
      <div>
        <span class="tog-label">${label}</span>
        ${hint && html`<div style="font-size:11px;color:var(--muted);margin-top:1px">${hint}</div>`}
        ${warn && checked && html`<div style="font-size:11px;color:var(--warn);margin-top:2px">⚠️ ${warn}</div>`}
      </div>
    </div>`;
}

function SharesView({ isAdmin }) {
  const [tab, setTab] = useState("cifs");
  const [cifsList, setCifsList] = useState([]);
  const [nfsList, setNfsList] = useState([]);
  const [volumes, setVolumes] = useState([]);
  const [appUsers, setAppUsers] = useState([]);
  const [appGroups, setAppGroups] = useState([]);
  const [loading, setLoading] = useState(true);
  const [apiErr, setApiErr] = useState(null);
  const [modal, setModal] = useState(null);
  const [panel, setPanel] = useState(null);
  const [toasts, showToast] = useToast();

  const loadAll = useCallback(() => {
    setLoading(true);
    Promise.all([
      api.listShares(),
      api.listFilesystems(),
      api.listUsers(),
      api.listGroups(),
    ]).then(([shareList, fsList, userList, groupList]) => {
      const vols = (fsList||[]).filter(fs=>fs.mount_point&&fs.fstype).map(fs=>({
        name: fs.device.replace("/dev/",""),
        raid: "RAID"+fs.level,
        mount: fs.mount_point,
        usedBytes: fs.used_bytes||0,
        state: fs.state,
      }));
      setVolumes(vols);
      const ulist = (userList||[]).map(u=>({ username:u.username, role:u.is_admin?"Admin":"User" }));
      setAppUsers(ulist);
      const glist = (groupList||[]).map(g=>g.name||g).filter(Boolean);
      setAppGroups(glist);
      const cifs = [], nfs = [];
      for (const dto of (shareList||[])) {
        const share = mapShareFromAPI(dto, ulist);
        if (dto.protocol==="smb") cifs.push(share); else nfs.push(share);
      }
      setCifsList(cifs);
      setNfsList(nfs);
      setApiErr(null);
    }).catch(e=>setApiErr(e.message)).finally(()=>setLoading(false));
  }, []);

  useEffect(()=>{ loadAll(); }, []);

  const cifsActive = cifsList.filter(s=>s.status==="active").length;
  const nfsActive = nfsList.filter(s=>s.status==="active").length;

  const handleCIFSSave = (data) => {
    const apiData = mapCIFSToAPI(data);
    const op = data.id ? api.updateShare(data.id,{...apiData,confirm:true}) : api.createShare(apiData);
    op.then(()=>{ showToast(data.id?`"${data.name}" updated`:`"${data.name}" created`); setModal(null); loadAll(); })
      .catch(e=>showToast(e.message||"Error saving share","error"));
  };

  const handleNFSSave = (data) => {
    const apiData = mapNFSToAPI(data);
    const op = data.id ? api.updateShare(data.id,{...apiData,confirm:true}) : api.createShare(apiData);
    op.then(()=>{ showToast(data.id?`"${data.name}" updated`:`"${data.name}" created`); setModal(null); loadAll(); })
      .catch(e=>showToast(e.message||"Error saving share","error"));
  };

  const handleDelete = (type, id, name) => {
    api.deleteShare(id)
      .then(()=>{ showToast(`"${name}" removed`); setModal(null); if(panel&&panel.share.id===id)setPanel(null); loadAll(); })
      .catch(e=>showToast(e.message||"Error deleting","error"));
  };

  const handleToggle = (type, id) => {
    const list = type==="cifs"?cifsList:nfsList;
    const s = list.find(x=>x.id===id);
    if (!s) return;
    const apiData = type==="cifs" ? mapCIFSToAPI(s) : mapNFSToAPI(s);
    api.updateShare(id,{...apiData, enabled:s.status!=="active", confirm:true})
      .then(()=>{ showToast(s.status==="active"?`"${s.name}" disabled`:`"${s.name}" enabled`); loadAll(); })
      .catch(e=>showToast(e.message||"Error","error"));
  };

  if (loading && !cifsList.length && !nfsList.length) return html`
    <section><header class="page-head"><h1>Share Management</h1></header>
    <p class="muted" style="margin-top:24px">Loading…</p></section>`;

  return html`
    <section class="shares-view">
      <header class="page-head"><h1>Share Management</h1></header>
      ${apiErr && html`<div class="error-box" style="margin-bottom:16px">⚠️ ${apiErr} <button class="link" onClick=${loadAll}>Retry</button></div>`}
      <div class="stats-row">
        <div class="stat-card"><span class="stat-icon">📁</span><div><span class="stat-val">${cifsList.length+nfsList.length}</span><span class="stat-label">Total Shares</span></div></div>
        <div class="stat-card"><span class="stat-icon">🔵</span><div><span class="stat-val" style="color:var(--primary)">${cifsActive}</span><span class="stat-label">CIFS Active</span></div></div>
        <div class="stat-card"><span class="stat-icon">🟢</span><div><span class="stat-val" style="color:#10b981">${nfsActive}</span><span class="stat-label">NFS Active</span></div></div>
        <div class="stat-card"><span class="stat-icon">💾</span><div><span class="stat-val">${volumes.length}</span><span class="stat-label">Volumes</span></div></div>
      </div>
      <div class="panel-tabs">
        <button class=${"panel-tab"+(tab==="cifs"?" active":"")} onClick=${()=>setTab("cifs")}>🔵 CIFS Shares (SMB/Windows)</button>
        <button class=${"panel-tab"+(tab==="nfs"?" active":"")} onClick=${()=>setTab("nfs")}>🟢 NFS Shares (Linux/Mac/Unix)</button>
      </div>
      ${tab==="cifs" ? html`<${CIFSTab} shares=${cifsList} volumes=${volumes} isAdmin=${isAdmin}
        onEdit=${s=>setModal({type:"editCIFS",share:s})}
        onToggle=${id=>handleToggle("cifs",id)}
        onDelete=${s=>setModal({type:"deleteCIFS",share:s})}
        onCreate=${()=>setModal({type:"createCIFS"})}
        onRowClick=${s=>setPanel({type:"cifs",share:s})} />
      ` : html`<${NFSTab} shares=${nfsList} volumes=${volumes} isAdmin=${isAdmin}
        onEdit=${s=>setModal({type:"editNFS",share:s})}
        onRules=${s=>setModal({type:"rulesNFS",share:s})}
        onToggle=${id=>handleToggle("nfs",id)}
        onDelete=${s=>setModal({type:"deleteNFS",share:s})}
        onCreate=${()=>setModal({type:"createNFS"})}
        onRowClick=${s=>setPanel({type:"nfs",share:s})} />`}
      ${modal?.type==="createCIFS" && html`<${CIFSModal} volumes=${volumes} users=${appUsers} groups=${appGroups} onClose=${()=>setModal(null)} onSave=${handleCIFSSave} />`}
      ${modal?.type==="editCIFS" && html`<${CIFSModal} share=${modal.share} volumes=${volumes} users=${appUsers} groups=${appGroups} onClose=${()=>setModal(null)} onSave=${handleCIFSSave} />`}
      ${modal?.type==="createNFS" && html`<${NFSModal} volumes=${volumes} onClose=${()=>setModal(null)} onSave=${handleNFSSave} />`}
      ${modal?.type==="editNFS" && html`<${NFSModal} share=${modal.share} volumes=${volumes} onClose=${()=>setModal(null)} onSave=${handleNFSSave} />`}
      ${modal?.type==="rulesNFS" && html`<${ClientRulesModal} share=${modal.share} onClose=${()=>setModal(null)} onSave=${handleNFSSave} />`}
      ${(modal?.type==="deleteCIFS"||modal?.type==="deleteNFS") && html`<${DeleteShareModal}
        share=${modal.share} type=${modal.type==="deleteCIFS"?"cifs":"nfs"}
        onClose=${()=>setModal(null)}
        onConfirm=${()=>handleDelete(modal.type==="deleteCIFS"?"cifs":"nfs",modal.share.id,modal.share.name)} />`}
      ${panel && html`<${QuickInfoPanel} item=${panel} volumes=${volumes} onClose=${()=>setPanel(null)}
        onEdit=${()=>{ setModal({type:panel.type==="cifs"?"editCIFS":"editNFS",share:panel.share}); setPanel(null); }}
        onDisable=${()=>{ handleToggle(panel.type,panel.share.id); setPanel(null); }} />`}
      <${ToastList} toasts=${toasts} />
    </section>`;
}

function CIFSTab({ shares, volumes, isAdmin, onEdit, onToggle, onDelete, onCreate, onRowClick }) {
  const [search, setSearch] = useState("");
  const [filterVol, setFilterVol] = useState("");
  const [filterStatus, setFilterStatus] = useState("");
  const filtered = shares.filter(s => {
    const q = search.toLowerCase();
    return (!q||s.name.includes(q)||s.path.includes(q))&&(!filterVol||s.volume===filterVol)&&(!filterStatus||s.status===filterStatus);
  });
  const permBadges = (s) => {
    const out = [];
    (s.groups||[]).forEach(g => out.push(html`<span class=${"sh-perm-badge sh-perm-"+g.perm} style="margin:1px">${g.name}(${g.perm.toUpperCase()})</span>`));
    (s.users||[]).forEach(u => out.push(html`<span class=${"sh-perm-badge sh-perm-"+u.perm} style="margin:1px">${u.username}(${u.perm.toUpperCase()})</span>`));
    if (s.smb?.guest) out.push(html`<span class="sh-perm-badge" style="background:rgba(210,153,34,.12);color:var(--warn);margin:1px">guest</span>`);
    return out;
  };
  const quotaPct = (s) => {
    if (!s.quota?.enabled||!s.usedGB) return null;
    const tot = s.quota.size*(s.quota.unit==="TB"?1000:s.quota.unit==="MB"?.001:1);
    return Math.round((s.usedGB/tot)*100);
  };
  return html`
    <div>
      <div class="sh-toolbar">
        <div class="sh-search-wrap"><span class="search-icon">🔍</span><input class="sh-search" placeholder="Search name or path…" value=${search} onInput=${e=>setSearch(e.target.value)} /></div>
        <select class="sh-filter" value=${filterVol} onChange=${e=>setFilterVol(e.target.value)}>
          <option value="">All Volumes</option>${volumes.map(v=>html`<option value=${v.name}>${v.name} (${v.raid})</option>`)}
        </select>
        <select class="sh-filter" value=${filterStatus} onChange=${e=>setFilterStatus(e.target.value)}><option value="">All Status</option><option value="active">Active</option><option value="disabled">Disabled</option></select>
        ${isAdmin && html`<button class="primary" onClick=${onCreate}>+ Create CIFS Share</button>`}
      </div>
      <div class="table-wrap"><table class="data">
        <thead><tr><th>Name</th><th>Path</th><th>Volume</th><th>Quota / Used</th><th>Users & Groups</th><th>Guest</th><th>Status</th>${isAdmin&&html`<th>Actions</th>`}</tr></thead>
        <tbody>
          ${filtered.map(s => {
            const pct = quotaPct(s);
            return html`<tr class="us-row" style="cursor:pointer" onClick=${e=>{if(!e.target.closest("button"))onRowClick(s);}}>
              <td><span class="mono" style="font-weight:600">${s.name}</span>${s.smb?.timeMachine?html`<span style="font-size:10px;color:#a78bfa;margin-left:4px">🍎TM</span>`:""}</td>
              <td class="mono muted" style="font-size:12px">${s.path}</td>
              <td><span class="group-tag">${s.volume||"—"}</span></td>
              <td>${s.quota?.enabled?html`<div style="font-size:12px">${s.quota.size}${s.quota.unit}</div>${pct!==null?html`<div class="quota-bar-wrap"><div class="quota-bar"><div class="quota-bar-fill" style="width:${Math.min(pct,100)}%;background:${pct>90?"var(--danger)":pct>70?"var(--warn)":"var(--ok)"}"></div></div><span style="font-size:10px;color:var(--muted)">${pct}%</span></div>`:""}`:html`<span class="muted">—</span>`}${s.usedGB?html`<div style="font-size:11px;color:var(--muted)">${fmtSize(s.usedGB)} used</div>`:""}</td>
              <td><div style="display:flex;flex-wrap:wrap;gap:2px;align-items:center;padding:2px 0">${permBadges(s)}</div></td>
              <td>${s.smb?.guest?html`<span class="badge" style="background:rgba(210,153,34,.12);color:var(--warn)">Yes</span>`:html`<span class="muted">No</span>`}</td>
              <td><span class=${"badge "+(s.status==="active"?"active":"degraded")}>${s.status==="active"?"Active":"Disabled"}</span></td>
              ${isAdmin&&html`<td class="actions-cell" onClick=${e=>e.stopPropagation()}>
                <button class="action-btn" onClick=${()=>onEdit(s)}>✏️ Edit</button>
                <button class=${"action-btn"+(s.status==="active"?" warn-btn":"")} onClick=${()=>onToggle(s.id)}>${s.status==="active"?"🔴 Disable":"🟢 Enable"}</button>
                <button class="action-btn danger-btn" onClick=${()=>onDelete(s)}>🗑️ Delete</button>
              </td>`}
            </tr>`;
          })}
          ${filtered.length===0&&html`<tr><td colspan=${isAdmin?8:7} class="muted" style="text-align:center;padding:24px">${shares.length===0?"No CIFS shares yet. Create one to get started.":"No shares match the filter."}</td></tr>`}
        </tbody>
      </table></div>
    </div>`;
}

function NFSTab({ shares, volumes, isAdmin, onEdit, onRules, onToggle, onDelete, onCreate, onRowClick }) {
  const [search, setSearch] = useState("");
  const [filterVol, setFilterVol] = useState("");
  const [filterVer, setFilterVer] = useState("");
  const filtered = shares.filter(s => {
    const q = search.toLowerCase();
    const matchVer = !filterVer||(filterVer==="NFSv3"&&s.versions?.v3)||(filterVer==="NFSv4"&&s.versions?.v4)||(filterVer==="NFSv4.1"&&s.versions?.v41)||(filterVer==="NFSv4.2"&&s.versions?.v42);
    return (!q||s.name.includes(q)||s.path.includes(q))&&(!filterVol||s.volume===filterVol)&&matchVer;
  });
  const renderVers = v => [v?.v3&&"NFSv3",v?.v4&&"NFSv4",v?.v41&&"NFSv4.1",v?.v42&&"NFSv4.2"].filter(Boolean).join(", ")||"—";
  return html`
    <div>
      <div class="sh-toolbar">
        <div class="sh-search-wrap"><span class="search-icon">🔍</span><input class="sh-search" placeholder="Search name or path…" value=${search} onInput=${e=>setSearch(e.target.value)} /></div>
        <select class="sh-filter" value=${filterVol} onChange=${e=>setFilterVol(e.target.value)}>
          <option value="">All Volumes</option>${volumes.map(v=>html`<option value=${v.name}>${v.name}</option>`)}
        </select>
        <select class="sh-filter" value=${filterVer} onChange=${e=>setFilterVer(e.target.value)}><option value="">All NFS Versions</option>${["NFSv3","NFSv4","NFSv4.1","NFSv4.2"].map(v=>html`<option value=${v}>${v}</option>`)}</select>
        ${isAdmin && html`<button class="primary" style="background:#10b981" onClick=${onCreate}>+ Create NFS Share</button>`}
      </div>
      <div class="table-wrap"><table class="data">
        <thead><tr><th>Name</th><th>Export Path</th><th>Volume</th><th>Versions</th><th>Client Rules</th><th>Status</th>${isAdmin&&html`<th>Actions</th>`}</tr></thead>
        <tbody>
          ${filtered.map(s => html`
            <tr class="us-row" style="cursor:pointer" onClick=${e=>{if(!e.target.closest("button"))onRowClick(s);}}>
              <td class="mono" style="font-weight:600">${s.name}</td>
              <td class="mono muted" style="font-size:12px">${s.path}</td>
              <td><span class="group-tag">${s.volume||"—"}</span></td>
              <td style="font-size:12px">${renderVers(s.versions)}</td>
              <td><div style="display:flex;flex-direction:column;gap:3px;padding:4px 0">${(s.rules||[]).map(r=>html`<div style="font-size:11px;font-family:monospace"><span class=${"sh-perm-badge sh-perm-"+r.perm} style="margin-right:4px">${r.perm.toUpperCase()}</span>${r.client}</div>`)}</div></td>
              <td><span class=${"badge "+(s.status==="active"?"active":"degraded")}>${s.status==="active"?"Active":"Disabled"}</span></td>
              ${isAdmin&&html`<td class="actions-cell" onClick=${e=>e.stopPropagation()}>
                <button class="action-btn" onClick=${()=>onEdit(s)}>✏️ Edit</button>
                <button class="action-btn" onClick=${()=>onRules(s)}>📋 Rules</button>
                <button class=${"action-btn"+(s.status==="active"?" warn-btn":"")} onClick=${()=>onToggle(s.id)}>${s.status==="active"?"🔴 Disable":"🟢 Enable"}</button>
                <button class="action-btn danger-btn" onClick=${()=>onDelete(s)}>🗑️ Delete</button>
              </td>`}
            </tr>
          `)}
          ${filtered.length===0&&html`<tr><td colspan=${isAdmin?7:6} class="muted" style="text-align:center;padding:24px">${shares.length===0?"No NFS exports yet. Create one to get started.":"No shares match the filter."}</td></tr>`}
        </tbody>
      </table></div>
    </div>`;
}

function CIFSModal({ share, volumes, users, groups, onClose, onSave }) {
  const isEdit = !!share;
  const defVol = volumes[0]?.name||"";
  const [form, setForm] = useState(() => isEdit ? {
    ...share, smb:{...share.smb}, quota:{...share.quota},
    users:share.users.map(u=>({...u})), groups:share.groups.map(g=>({...g})),
  } : {
    name:"", description:"", volume:defVol, path:"", createFolder:true, recycleBin:false, hidden:false,
    quota:{ enabled:false, size:200, unit:"GB", warningPct:80 },
    smb:{ networkName:"", minProtocol:"SMB2", signing:false, encryption:false, timeMachine:false, guest:false, oplocks:"enabled", hideDotFiles:false, caseSensitivity:"insensitive", comment:"", logAccess:false },
    users:[], groups:[], defaultPerm:"none",
  });
  const [errors, setErrors] = useState({});
  const [picker, setPicker] = useState(false);
  useEscClose(onClose);
  const set = (k,v) => setForm(f=>({...f,[k]:v}));
  const setSMB = (k,v) => setForm(f=>({...f,smb:{...f.smb,[k]:v}}));
  const setQuota = (k,v) => setForm(f=>({...f,quota:{...f.quota,[k]:v}}));

  useEffect(()=>{
    if(!isEdit&&form.volume&&form.name){
      const vol = volumes.find(v=>v.name===form.volume);
      if(vol) set("path", vol.mount+"/"+form.name);
    }
  },[form.volume,form.name]);

  useEffect(()=>{ if(!isEdit) setSMB("networkName",form.name); },[form.name]);

  const validate = () => {
    const e = {};
    if(!form.name) e.name="Required";
    else if(/\s/.test(form.name)) e.name="No spaces allowed";
    if(!form.path) e.path="Required";
    return e;
  };
  const handleSave = () => { const e=validate(); setErrors(e); if(Object.keys(e).length)return; onSave(form); };

  const addUser = username => {
    const u = users.find(u=>u.username===username);
    if(!u||form.users.find(x=>x.username===username))return;
    setForm(f=>({...f,users:[...f.users,{username:u.username,role:u.role,perm:"rw"}]}));
  };
  const removeUser = u => setForm(f=>({...f,users:f.users.filter(x=>x.username!==u)}));
  const setUserPerm = (u,p) => setForm(f=>({...f,users:f.users.map(x=>x.username===u?{...x,perm:p}:x)}));
  const addGroup = name => { if(!name||form.groups.find(g=>g.name===name))return; setForm(f=>({...f,groups:[...f.groups,{name,perm:"rw"}]})); };
  const removeGroup = n => setForm(f=>({...f,groups:f.groups.filter(g=>g.name!==n)}));
  const setGroupPerm = (n,p) => setForm(f=>({...f,groups:f.groups.map(g=>g.name===n?{...g,perm:p}:g)}));

  const avUsers = users.filter(u=>!form.users.find(x=>x.username===u.username));
  const avGroups = groups.filter(g=>!form.groups.find(x=>x.name===g));
  const quotaGB = form.quota.size*(form.quota.unit==="TB"?1000:form.quota.unit==="MB"?.001:1);
  const usedPct = share?.usedGB?Math.round((share.usedGB/quotaGB)*100):0;
  const permOpts = [{value:"none",label:"No Access"},{value:"ro",label:"Read Only"},{value:"rw",label:"Read & Write"},{value:"full",label:"Full Control"}];

  return html`
    <div class="modal-overlay" onClick=${e=>e.target===e.currentTarget&&onClose()}>
      <div class="modal-card modal-lg" style="max-width:980px">
        <div class="modal-head"><h2>${isEdit?"✏️ Edit CIFS Share":"➕ Create CIFS Share"}</h2><button class="link" onClick=${onClose}>✕</button></div>

        <p class="modal-section-title">📋 Basic Information</p>
        <div class="form-grid-3">
          <label>Share Name ${!isEdit&&html`<span class="required">*</span>`}
            <input value=${form.name} disabled=${isEdit} class=${errors.name?"input-error":""} placeholder="e.g. documents"
              onInput=${e=>{set("name",e.target.value);setErrors(x=>({...x,name:""}));}} />
            ${errors.name&&html`<span class="field-error">${errors.name}</span>`}
          </label>
          <label>Description<input value=${form.description} placeholder="Optional" onInput=${e=>set("description",e.target.value)} /></label>
          <label>Volume
            <select value=${form.volume} onChange=${e=>set("volume",e.target.value)}>
              ${volumes.length===0?html`<option value="">No volumes mounted</option>`:volumes.map(v=>html`<option value=${v.name}>${v.name} — ${v.raid} (${v.mount})</option>`)}
            </select>
          </label>
          <label style="grid-column:1/-1">Path <span class="required">*</span>
            <div style="display:flex;gap:8px">
              <input value=${form.path} class=${errors.path?"input-error":""} placeholder="/srv/nas/md0/sharename" style="flex:1"
                onInput=${e=>{set("path",e.target.value);setErrors(x=>({...x,path:""}));}} />
              <button type="button" class="action-btn" onClick=${()=>setPicker(true)}>📁 Browse</button>
            </div>
            ${errors.path&&html`<span class="field-error">${errors.path}</span>`}
          </label>
        </div>
        ${picker&&html`<${FolderPicker} onClose=${()=>setPicker(false)}
          onSelect=${p=>{set("path",p);setErrors(x=>({...x,path:""}));setPicker(false);}} />`}
        <div style="display:flex;gap:20px;margin-top:12px;flex-wrap:wrap">
          <${TogRow} label="Create folder if not exists" checked=${form.createFolder} onChange=${e=>set("createFolder",e.target.checked)} />
          <${TogRow} label="Enable Recycle Bin" hint="Deleted files go to .recycle inside the share" checked=${form.recycleBin} onChange=${e=>set("recycleBin",e.target.checked)} />
          <${TogRow} label="Hidden Share" hint="Accessible only via direct path" checked=${form.hidden} onChange=${e=>set("hidden",e.target.checked)} />
        </div>

        <div class="modal-section">
          <p class="modal-section-title">📊 Quota</p>
          <${TogRow} label="Enable Quota" checked=${form.quota.enabled} onChange=${e=>setQuota("enabled",e.target.checked)} />
          ${form.quota.enabled && html`
            <div class="form-grid-3" style="margin-top:10px">
              <label>Maximum Size
                <div style="display:flex;gap:6px;margin-top:4px">
                  <input type="number" value=${form.quota.size} min="1" style="flex:1;background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);padding:8px 10px;font-size:14px" onInput=${e=>setQuota("size",+e.target.value)} />
                  <select value=${form.quota.unit} onChange=${e=>setQuota("unit",e.target.value)} style="background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);padding:8px 10px;font-size:14px"><option>MB</option><option>GB</option><option>TB</option></select>
                </div>
              </label>
              <label>Warning Threshold: <strong>${form.quota.warningPct}%</strong>
                <input type="range" min="50" max="95" step="5" value=${form.quota.warningPct} style="width:100%;margin-top:8px;accent-color:var(--primary)" onInput=${e=>setQuota("warningPct",+e.target.value)} />
                <div style="display:flex;justify-content:space-between;font-size:10px;color:var(--muted)"><span>50%</span><span>95%</span></div>
              </label>
              ${isEdit && html`
                <div>
                  <div style="font-size:12px;color:var(--muted);margin-bottom:4px">Current usage</div>
                  <div class="quota-bar-wrap"><div class="quota-bar"><div class="quota-bar-fill" style="width:${Math.min(usedPct,100)}%;background:${usedPct>90?"var(--danger)":usedPct>form.quota.warningPct?"var(--warn)":"var(--ok)"}"></div></div><span style="font-size:12px;font-weight:600">${usedPct}%</span></div>
                  <div style="font-size:11px;color:var(--muted)">${fmtSize(share?.usedGB||0)} / ${form.quota.size}${form.quota.unit}</div>
                </div>`}
            </div>`}
        </div>

        <div class="modal-section">
          <p class="modal-section-title">⚙️ SMB/CIFS Parameters</p>
          <div class="form-grid-3" style="margin-bottom:12px">
            <label>Network Share Name<input value=${form.smb.networkName} placeholder=${form.name||"sharename"} onInput=${e=>setSMB("networkName",e.target.value)} /></label>
            <label>Min Protocol<select value=${form.smb.minProtocol} onChange=${e=>{setSMB("minProtocol",e.target.value);if(!e.target.value.startsWith("SMB3"))setSMB("encryption",false);}}>
              ${["SMB2","SMB2.1","SMB3","SMB3.1.1"].map(p=>html`<option value=${p}>${p}</option>`)}
            </select></label>
            <label>Oplocks<select value=${form.smb.oplocks} onChange=${e=>setSMB("oplocks",e.target.value)}>
              <option value="enabled">Enabled</option><option value="disabled">Disabled</option><option value="lease">Lease</option>
            </select></label>
            <label>Case Sensitivity<select value=${form.smb.caseSensitivity} onChange=${e=>setSMB("caseSensitivity",e.target.value)}>
              <option value="insensitive">Insensitive</option><option value="sensitive">Sensitive</option><option value="auto">Auto</option>
            </select></label>
            <label style="grid-column:span 2">Comment<input value=${form.smb.comment} placeholder="Visible in network browser" onInput=${e=>setSMB("comment",e.target.value)} /></label>
          </div>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:4px">
            <${TogRow} label="Require SMB Signing" hint="Increases security, minor performance impact" checked=${form.smb.signing} onChange=${e=>setSMB("signing",e.target.checked)} />
            <${TogRow} label="SMB Encryption (SMB3+ only)" disabled=${!form.smb.minProtocol.startsWith("SMB3")} checked=${form.smb.encryption} onChange=${e=>setSMB("encryption",e.target.checked)} />
            <${TogRow} label="Enable Time Machine" hint="macOS Time Machine backups" checked=${form.smb.timeMachine} onChange=${e=>setSMB("timeMachine",e.target.checked)} />
            <${TogRow} label="Allow Guest Access" warn="Anyone on the network can access without password" checked=${form.smb.guest} onChange=${e=>setSMB("guest",e.target.checked)} />
            <${TogRow} label="Hide Dot Files" hint="Files starting with . are hidden from Windows" checked=${form.smb.hideDotFiles} onChange=${e=>setSMB("hideDotFiles",e.target.checked)} />
            <${TogRow} label="Log Access" hint="Record every access to this share" checked=${form.smb.logAccess} onChange=${e=>setSMB("logAccess",e.target.checked)} />
          </div>
        </div>

        <div class="modal-section">
          <p class="modal-section-title">🔐 User & Group Permissions</p>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:20px">
            <div>
              <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
                <span style="font-size:13px;font-weight:600">Users</span>
                ${avUsers.length>0&&html`<select class="sh-filter" style="font-size:12px" onChange=${e=>{addUser(e.target.value);e.target.value="";}}>
                  <option value="">+ Add User</option>${avUsers.map(u=>html`<option value=${u.username}>${u.username} (${u.role})</option>`)}
                </select>`}
              </div>
              ${form.users.length===0?html`<p class="muted" style="font-size:12px">No users added</p>`:html`
                <table class="perm-table">
                  <thead><tr><th>Username</th><th>Role</th><th>Permission</th><th></th></tr></thead>
                  <tbody>${form.users.map(u=>html`<tr>
                    <td class="mono">${u.username}</td>
                    <td><${RoleBadge} role=${u.role} /></td>
                    <td><select value=${u.perm} onChange=${e=>setUserPerm(u.username,e.target.value)}>${permOpts.map(o=>html`<option value=${o.value}>${o.label}</option>`)}</select></td>
                    <td><button class="link" style="color:var(--danger)" onClick=${()=>removeUser(u.username)}>🗑️</button></td>
                  </tr>`)}</tbody>
                </table>`}
            </div>
            <div>
              <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
                <span style="font-size:13px;font-weight:600">Groups</span>
                ${avGroups.length>0&&html`<select class="sh-filter" style="font-size:12px" onChange=${e=>{addGroup(e.target.value);e.target.value="";}}>
                  <option value="">+ Add Group</option>${avGroups.map(g=>html`<option value=${g}>${g}</option>`)}
                </select>`}
              </div>
              ${form.groups.length===0?html`<p class="muted" style="font-size:12px">No groups added</p>`:html`
                <table class="perm-table">
                  <thead><tr><th>Group</th><th>Permission</th><th></th></tr></thead>
                  <tbody>${form.groups.map(g=>html`<tr>
                    <td class="mono">${g.name}</td>
                    <td><select value=${g.perm} onChange=${e=>setGroupPerm(g.name,e.target.value)}>${permOpts.map(o=>html`<option value=${o.value}>${o.label}</option>`)}</select></td>
                    <td><button class="link" style="color:var(--danger)" onClick=${()=>removeGroup(g.name)}>🗑️</button></td>
                  </tr>`)}</tbody>
                </table>`}
            </div>
          </div>
          <div style="margin-top:12px;padding:10px;background:var(--surface-2);border-radius:var(--radius);font-size:12px;color:var(--muted)">
            ℹ️ In case of conflict between user and group permissions, the more restrictive applies.
          </div>
          <div style="margin-top:10px;display:flex;align-items:center;gap:10px;font-size:13px">
            <span style="color:var(--muted)">Default access for unlisted users:</span>
            <select value=${form.defaultPerm} onChange=${e=>set("defaultPerm",e.target.value)} style="background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);padding:6px 10px">
              <option value="none">No Access</option><option value="ro">Read Only</option><option value="rw">Read & Write</option>
            </select>
          </div>
        </div>

        ${isEdit&&html`<p class="muted" style="font-size:12px;margin-top:16px;border-top:1px solid var(--border);padding-top:10px">Created: ${share.createdAt||"—"} — Last modified: ${share.modifiedAt||"—"}</p>`}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" onClick=${handleSave}>${isEdit?"Save Changes":"Create Share"}</button>
        </div>
      </div>
    </div>`;
}

function NFSRulesEditor({ rules, setRules, exportPath }) {
  const defaultAdv = { sync:true, noRootSquash:false, rootSquash:true, allSquash:false, anonuid:65534, anongid:65534, noSubtreeCheck:true, secure:true, crossmnt:false };
  const defaultRule = () => ({ id:Date.now()+Math.random(), client:"", perm:"rw", advOpen:false, adv:{...defaultAdv} });
  const addRule = () => setRules(prev=>[...prev, defaultRule()]);
  const removeRule = idx => setRules(prev=>prev.filter((_,i)=>i!==idx));
  const updateRule = (idx,k,v) => setRules(prev=>prev.map((r,i)=>i===idx?{...r,[k]:v}:r));
  const updateAdv = (idx,k,v) => setRules(prev=>prev.map((r,i)=>i===idx?{...r,adv:{...r.adv,[k]:v}}:r));
  const toggleAdv = idx => setRules(prev=>prev.map((r,i)=>i===idx?{...r,advOpen:!r.advOpen}:r));
  const moveRule = (idx,dir) => {
    const r=[...rules], ni=idx+dir;
    if(ni<0||ni>=r.length) return;
    [r[idx],r[ni]]=[r[ni],r[idx]]; setRules(r);
  };
  const isValidClient = v => !v||[/^\*$/,/^\d{1,3}(\.\d{1,3}){3}$/,/^\d{1,3}(\.\d{1,3}){3}\/\d{1,2}$/,/^[a-zA-Z0-9._-]+$/,/^\d{1,3}(\.\d{1,3}){3}-\d{1,3}(\.\d{1,3}){3}$/].some(p=>p.test(v));
  const preview = rules.map(r=>{
    if(!r.client) return "";
    const opts=[r.perm];
    if(r.adv.sync) opts.push("sync"); else opts.push("async");
    if(r.adv.noRootSquash) opts.push("no_root_squash"); else if(r.adv.rootSquash) opts.push("root_squash");
    if(r.adv.allSquash) opts.push("all_squash");
    if(r.adv.noSubtreeCheck) opts.push("no_subtree_check");
    if(!r.adv.secure) opts.push("insecure");
    if(r.adv.crossmnt) opts.push("crossmnt");
    return (exportPath||"/mnt/...")+" "+r.client+"("+opts.join(",")+")";
  }).filter(Boolean).join("\n")||"# Add client rules above";

  return html`
    <div>
      ${rules.map((rule,idx)=>html`
        <div class="rule-row" key=${rule.id}>
          <div class="rule-row-head">
            <div class="rule-priority">
              <button type="button" onClick=${()=>moveRule(idx,-1)} disabled=${idx===0} style="cursor:${idx===0?"not-allowed":"pointer"};opacity:${idx===0?.4:1}">↑</button>
              <button type="button" onClick=${()=>moveRule(idx,1)} disabled=${idx===rules.length-1} style="cursor:${idx===rules.length-1?"not-allowed":"pointer"};opacity:${idx===rules.length-1?.4:1}">↓</button>
              <span style="font-size:11px;color:var(--muted);margin-left:4px">#${idx+1}</span>
            </div>
            <div style="flex:1;min-width:160px">
              <input value=${rule.client} placeholder="IP, subnet (1.2.3.0/24), *, hostname"
                style=${"background:var(--bg);border:1px solid "+(rule.client&&!isValidClient(rule.client)?"var(--danger)":"var(--border)")+";border-radius:var(--radius);color:var(--text);padding:7px 10px;width:100%;font-size:13px;box-sizing:border-box"}
                onInput=${e=>updateRule(idx,"client",e.target.value)} />
              ${rule.client&&!isValidClient(rule.client)&&html`<span class="field-error">Invalid format</span>`}
            </div>
            <select class="sh-filter" value=${rule.perm} onChange=${e=>updateRule(idx,"perm",e.target.value)}>
              <option value="ro">ro — Read Only</option><option value="rw">rw — Read & Write</option>
            </select>
            <button type="button" class="adv-toggle" onClick=${()=>toggleAdv(idx)}>${rule.advOpen?"▾":"▸"} Advanced</button>
            ${rules.length>1&&html`<button class="link" style="color:var(--danger);font-size:14px;padding:0 4px" onClick=${()=>removeRule(idx)}>✕</button>`}
          </div>
          ${rule.advOpen&&html`
            <div class="rule-adv">
              <${TogRow} label="sync" hint="Guarantees data integrity; async = better performance" checked=${rule.adv.sync} onChange=${e=>updateAdv(idx,"sync",e.target.checked)} />
              <${TogRow} label="no_root_squash" warn="Root on client will have root access on the share" checked=${rule.adv.noRootSquash} onChange=${e=>updateAdv(idx,"noRootSquash",e.target.checked)} />
              <${TogRow} label="root_squash" hint="Map root to anonymous user (default, secure)" checked=${rule.adv.rootSquash} onChange=${e=>updateAdv(idx,"rootSquash",e.target.checked)} />
              <${TogRow} label="all_squash" hint="Map all users to anonymous user" checked=${rule.adv.allSquash} onChange=${e=>updateAdv(idx,"allSquash",e.target.checked)} />
              <${TogRow} label="no_subtree_check" hint="Disable subtree checking (better performance)" checked=${rule.adv.noSubtreeCheck} onChange=${e=>updateAdv(idx,"noSubtreeCheck",e.target.checked)} />
              <${TogRow} label="secure" hint="Require client ports < 1024" checked=${rule.adv.secure} onChange=${e=>updateAdv(idx,"secure",e.target.checked)} />
              <${TogRow} label="crossmnt" hint="Allow traversal of mount points" checked=${rule.adv.crossmnt} onChange=${e=>updateAdv(idx,"crossmnt",e.target.checked)} />
              ${(rule.adv.allSquash||rule.adv.noRootSquash)&&html`
                <div class="rule-adv-num"><span>anonuid:</span><input type="number" value=${rule.adv.anonuid} style="background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);padding:4px 8px;width:80px" onInput=${e=>updateAdv(idx,"anonuid",+e.target.value)} /></div>
                <div class="rule-adv-num"><span>anongid:</span><input type="number" value=${rule.adv.anongid} style="background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);padding:4px 8px;width:80px" onInput=${e=>updateAdv(idx,"anongid",+e.target.value)} /></div>
              `}
            </div>`}
        </div>
      `)}
      <button class="action-btn" style="margin-top:4px" onClick=${addRule}>+ Add Client Rule</button>
      <div style="margin-top:14px">
        <p class="modal-section-title" style="margin-bottom:4px">/etc/exports preview</p>
        <pre class="exports-preview">${preview}</pre>
      </div>
    </div>`;
}

function NFSModal({ share, volumes, onClose, onSave }) {
  const isEdit = !!share;
  const defVol = volumes[0]?.name||"";
  const defaultAdv = { sync:true, noRootSquash:false, rootSquash:true, allSquash:false, anonuid:65534, anongid:65534, noSubtreeCheck:true, secure:true, crossmnt:false };
  const [form, setForm] = useState(() => isEdit ? {
    ...share, versions:{...share.versions}, nfsv4:{...share.nfsv4},
    rules:share.rules.map(r=>({...r,adv:{...r.adv}})), global:{...share.global},
  } : {
    name:"", description:"", volume:defVol, path:"", createFolder:true, logAccess:false,
    versions:{ v3:true, v4:true, v41:false, v42:false },
    nfsv4:{ domain:"nas.local", kerberos:false, kdc:"", realm:"", kerberosMode:"krb5" },
    rules:[{ id:1, client:"", perm:"rw", advOpen:false, adv:{...defaultAdv} }],
    global:{ fsid:false, fsidNum:0, acl:false, pnfs:false, sec:"sys", description:"" },
  });
  const [errors, setErrors] = useState({});
  const [picker, setPicker] = useState(false);
  useEscClose(onClose);
  const set = (k,v) => setForm(f=>({...f,[k]:v}));
  const setVer = (k,v) => setForm(f=>({...f,versions:{...f.versions,[k]:v}}));
  const setNFS4 = (k,v) => setForm(f=>({...f,nfsv4:{...f.nfsv4,[k]:v}}));
  const setGlobal = (k,v) => setForm(f=>({...f,global:{...f.global,[k]:v}}));
  // Accetta sia un array sia un updater funzionale (NFSRulesEditor usa prev=>...).
  const setRules = updater => setForm(f=>({...f, rules: typeof updater==="function" ? updater(f.rules) : updater}));

  useEffect(()=>{
    if(!isEdit&&form.volume&&form.name){const vol=volumes.find(v=>v.name===form.volume);if(vol)set("path",vol.mount+"/"+form.name);}
  },[form.volume,form.name]);

  const hasNFS4 = form.versions.v4||form.versions.v41||form.versions.v42;
  const validate = ()=>{ const e={}; if(!form.name)e.name="Required"; if(!form.path)e.path="Required"; return e; };
  const handleSave = ()=>{ const e=validate(); setErrors(e); if(Object.keys(e).length)return; onSave(form); };

  return html`
    <div class="modal-overlay" onClick=${e=>e.target===e.currentTarget&&onClose()}>
      <div class="modal-card modal-lg" style="max-width:980px">
        <div class="modal-head"><h2>${isEdit?"✏️ Edit NFS Share":"➕ Create NFS Share"}</h2><button class="link" onClick=${onClose}>✕</button></div>

        <p class="modal-section-title">📋 Basic Information</p>
        <div class="form-grid-3">
          <label>Export Name ${!isEdit&&html`<span class="required">*</span>`}
            <input value=${form.name} disabled=${isEdit} class=${errors.name?"input-error":""} placeholder="e.g. backup_nfs"
              onInput=${e=>{set("name",e.target.value);setErrors(x=>({...x,name:""}));}} />
            ${errors.name&&html`<span class="field-error">${errors.name}</span>`}
          </label>
          <label>Volume<select value=${form.volume} onChange=${e=>set("volume",e.target.value)}>
            ${volumes.length===0?html`<option value="">No volumes mounted</option>`:volumes.map(v=>html`<option value=${v.name}>${v.name} — ${v.raid} (${v.mount})</option>`)}
          </select></label>
          <label>Description<input value=${form.description} placeholder="Optional" onInput=${e=>set("description",e.target.value)} /></label>
          <label style="grid-column:1/-1">Export Path <span class="required">*</span>
            <div style="display:flex;gap:8px">
              <input value=${form.path} class=${errors.path?"input-error":""} placeholder="/srv/nas/md0/backup" style="flex:1"
                onInput=${e=>{set("path",e.target.value);setErrors(x=>({...x,path:""}));}} />
              <button type="button" class="action-btn" onClick=${()=>setPicker(true)}>📁 Browse</button>
            </div>
          </label>
        </div>
        ${picker&&html`<${FolderPicker} onClose=${()=>setPicker(false)}
          onSelect=${p=>{set("path",p);setErrors(x=>({...x,path:""}));setPicker(false);}} />`}
        <div style="display:flex;gap:20px;margin-top:10px;flex-wrap:wrap">
          <${TogRow} label="Create folder if not exists" checked=${form.createFolder} onChange=${e=>set("createFolder",e.target.checked)} />
          <${TogRow} label="Log access" checked=${form.logAccess} onChange=${e=>set("logAccess",e.target.checked)} />
        </div>

        <div class="modal-section">
          <p class="modal-section-title">📡 NFS Versions</p>
          <div style="display:flex;gap:16px;flex-wrap:wrap;margin-bottom:12px">
            ${[["v3","NFSv3"],["v4","NFSv4"],["v41","NFSv4.1"],["v42","NFSv4.2"]].map(([k,label])=>html`
              <label class="disk-row" style="padding:8px 12px"><input type="checkbox" checked=${form.versions[k]} onChange=${e=>setVer(k,e.target.checked)} /><span>${label}</span></label>
            `)}
          </div>
          ${hasNFS4&&html`
            <div class="form-grid-3" style="margin-bottom:10px">
              <label>NFSv4 Domain<input value=${form.nfsv4.domain} placeholder="nas.local" onInput=${e=>setNFS4("domain",e.target.value)} /></label>
            </div>
            <${TogRow} label="Enable Kerberos (krb5)" checked=${form.nfsv4.kerberos} onChange=${e=>setNFS4("kerberos",e.target.checked)} />
            ${form.nfsv4.kerberos&&html`
              <div class="form-grid-3" style="margin-top:10px">
                <label>KDC Hostname<input value=${form.nfsv4.kdc} placeholder="kdc.domain.local" onInput=${e=>setNFS4("kdc",e.target.value)} /></label>
                <label>Realm<input value=${form.nfsv4.realm} placeholder="DOMAIN.LOCAL" onInput=${e=>setNFS4("realm",e.target.value)} /></label>
                <label>Kerberos Security<select value=${form.nfsv4.kerberosMode} onChange=${e=>setNFS4("kerberosMode",e.target.value)}>
                  <option value="krb5">krb5 (auth only)</option><option value="krb5i">krb5i (+ integrity)</option><option value="krb5p">krb5p (+ encryption)</option>
                </select></label>
              </div>`}
          `}
        </div>

        <div class="modal-section">
          <p class="modal-section-title">🔒 Client Access Rules</p>
          <${NFSRulesEditor} rules=${form.rules} setRules=${setRules} exportPath=${form.path||"/mnt/..."} />
        </div>

        <div class="modal-section">
          <p class="modal-section-title">🌐 Global Export Options</p>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:4px;margin-bottom:10px">
            <${TogRow} label="fsid (NFSv4 root export)" checked=${form.global.fsid} onChange=${e=>setGlobal("fsid",e.target.checked)} />
            <${TogRow} label="Enable ACL (NFSv4)" disabled=${!hasNFS4} checked=${form.global.acl} onChange=${e=>setGlobal("acl",e.target.checked)} />
            <${TogRow} label="pNFS (Parallel NFS, NFSv4.1+)" disabled=${!form.versions.v41&&!form.versions.v42} checked=${form.global.pnfs} onChange=${e=>setGlobal("pnfs",e.target.checked)} />
          </div>
          <div class="form-grid-3">
            <label>Security (sec)<select value=${form.global.sec} onChange=${e=>setGlobal("sec",e.target.value)} style="background:var(--bg);border:1px solid var(--border);border-radius:var(--radius);color:var(--text);padding:8px 10px;width:100%;margin-top:4px;font-size:14px">
              <option value="sys">sys (Unix UID/GID)</option><option value="krb5">krb5 (Kerberos auth)</option><option value="krb5i">krb5i (+ integrity)</option><option value="krb5p">krb5p (+ encryption)</option>
            </select></label>
          </div>
        </div>

        ${isEdit&&html`<p class="muted" style="font-size:12px;margin-top:16px;border-top:1px solid var(--border);padding-top:10px">Created: ${share.createdAt||"—"} — Last modified: ${share.modifiedAt||"—"}</p>`}
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" style="background:#10b981" onClick=${handleSave}>${isEdit?"Save Changes":"Create Export"}</button>
        </div>
      </div>
    </div>`;
}

function ClientRulesModal({ share, onClose, onSave }) {
  const [rules, setRules] = useState(() => share.rules.map(r=>({...r,adv:{...r.adv}})));
  useEscClose(onClose);
  return html`
    <div class="modal-overlay" onClick=${e=>e.target===e.currentTarget&&onClose()}>
      <div class="modal-card modal-lg" style="max-width:880px">
        <div class="modal-head"><h2>📋 Client Rules — <span class="mono">${share.name}</span></h2><button class="link" onClick=${onClose}>✕</button></div>
        <p class="muted" style="font-size:12px;margin-bottom:14px">${share.path}</p>
        <${NFSRulesEditor} rules=${rules} setRules=${setRules} exportPath=${share.path} />
        <div class="form-actions" style="margin-top:16px">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary" style="background:#10b981" onClick=${()=>onSave({...share,rules})}>Save Rules</button>
        </div>
      </div>
    </div>`;
}

function DeleteShareModal({ share, type, onClose, onConfirm }) {
  const [confirmed, setConfirmed] = useState(false);
  useEscClose(onClose);
  return html`
    <div class="modal-overlay" onClick=${e=>e.target===e.currentTarget&&onClose()}>
      <div class="modal-card">
        <div class="modal-head"><h2>🗑️ Remove Share</h2><button class="link" onClick=${onClose}>✕</button></div>
        <p>The share <strong class="mono">${share.name}</strong> will be removed. <strong>Files on the volume will NOT be deleted.</strong></p>
        ${share.connections>0&&html`<div class="error-box" style="margin:12px 0">⚠️ ${share.connections} client${share.connections!==1?"s":""} currently connected will be disconnected.</div>`}
        <div class="info-box" style="margin:12px 0;font-size:13px">
          <div style="color:var(--muted);margin-bottom:6px">Access will be lost for:</div>
          ${type==="cifs"?html`
            <div style="font-size:12px;margin-bottom:4px">UNC: <code class="mono">\\NAS\${share.name}</code></div>
            ${(share.users||[]).map(u=>html`<div class="mono" style="font-size:12px;color:var(--muted)"> 👤 ${u.username} (${u.perm.toUpperCase()})</div>`)}
            ${(share.groups||[]).map(g=>html`<div class="mono" style="font-size:12px;color:var(--muted)"> 👥 ${g.name} (${g.perm.toUpperCase()})</div>`)}
          `:html`
            <div style="font-size:12px;margin-bottom:4px">Export: <code class="mono">${share.path}</code></div>
            ${(share.rules||[]).map(r=>html`<div class="mono" style="font-size:12px;color:var(--muted)"> ${r.client} (${r.perm.toUpperCase()})</div>`)}
          `}
        </div>
        <label class="disk-row" style="margin:16px 0">
          <input type="checkbox" checked=${confirmed} onChange=${e=>setConfirmed(e.target.checked)} />
          <span>I confirm I want to remove this share</span>
        </label>
        <div class="form-actions">
          <button class="link" onClick=${onClose}>Cancel</button>
          <button class="primary danger-primary" disabled=${!confirmed} onClick=${onConfirm}>Delete Share</button>
        </div>
      </div>
    </div>`;
}

function QuickInfoPanel({ item, volumes, onClose, onEdit, onDisable }) {
  const { type, share } = item;
  const vol = volumes.find(v=>v.name===share.volume);
  return html`
    <div class=${"slide-panel-overlay open"} onClick=${e=>{if(e.target.classList.contains("slide-panel-overlay")||e.target.classList.contains("slide-panel-backdrop"))onClose();}}>
      <div class="slide-panel-backdrop"></div>
      <div class="slide-panel">
        <div class="slide-panel-head">
          <div>
            <h3 class="slide-panel-title">${type==="cifs"?"🔵":"🟢"} ${share.name}</h3>
            <p class="slide-panel-sub">${share.path}</p>
          </div>
          <button class="link" onClick=${onClose}>✕</button>
        </div>
        <div class="sp-section">
          <p class="sp-label">Volume</p>
          <div style="font-size:13px;margin-bottom:4px">${share.volume||"—"}${vol?html` — ${vol.raid} (${vol.mount})`:""}</div>
          ${vol&&html`<div style="font-size:11px;color:var(--muted)">${vol.state}</div>`}
        </div>
        ${type==="cifs"?html`
          <div class="sp-section">
            <p class="sp-label">UNC Path</p>
            <code style="font-size:12px;color:var(--primary);font-family:monospace">\\NAS\${share.name}</code>
          </div>
          ${(share.users||[]).length>0&&html`<div class="sp-section"><p class="sp-label">Users</p>${share.users.map(u=>html`<div class="sp-row"><span class="mono" style="font-size:13px">${u.username}</span><span class=${"sh-perm-badge sh-perm-"+u.perm}>${u.perm.toUpperCase()}</span></div>`)}</div>`}
          ${(share.groups||[]).length>0&&html`<div class="sp-section"><p class="sp-label">Groups</p>${share.groups.map(g=>html`<div class="sp-row"><span class="mono" style="font-size:13px">${g.name}</span><span class=${"sh-perm-badge sh-perm-"+g.perm}>${g.perm.toUpperCase()}</span></div>`)}</div>`}
          <div class="sp-section"><p class="sp-label">SMB</p>
            ${share.smb?.timeMachine&&html`<div style="font-size:12px">🍎 Time Machine enabled</div>`}
            ${share.smb?.guest&&html`<div style="font-size:12px;color:var(--warn)">👤 Guest access enabled</div>`}
            ${share.smb?.encryption&&html`<div style="font-size:12px">🔒 Encrypted (SMB3)</div>`}
            <div style="font-size:12px;color:var(--muted)">Protocol: ${share.smb?.minProtocol||"SMB2"}+</div>
          </div>
          ${share.quota?.enabled&&html`<div class="sp-section"><p class="sp-label">Quota</p>
            <div style="font-size:13px">${share.quota.size}${share.quota.unit}</div>
          </div>`}
        `:html`
          <div class="sp-section"><p class="sp-label">/etc/exports</p>
            <pre class="exports-preview" style="margin:0;font-size:11px">${(share.rules||[]).map(r=>r.client?share.path+" "+r.client+"("+r.perm+")":"").filter(Boolean).join("\n")||"# no rules"}</pre>
          </div>
          <div class="sp-section"><p class="sp-label">Client Rules</p>${(share.rules||[]).map(r=>html`<div class="sp-row"><span class="mono" style="font-size:12px">${r.client}</span><span class=${"sh-perm-badge sh-perm-"+r.perm}>${r.perm.toUpperCase()}</span></div>`)}</div>
          <div class="sp-section"><p class="sp-label">NFS Versions</p><div style="font-size:13px">${[share.versions?.v3&&"NFSv3",share.versions?.v4&&"NFSv4",share.versions?.v41&&"NFSv4.1",share.versions?.v42&&"NFSv4.2"].filter(Boolean).join(", ")||"—"}</div></div>
        `}
        <div class="sp-quick-actions">
          <button class="action-btn" style="flex:1;text-align:center" onClick=${onEdit}>✏️ Edit</button>
          <button class=${"action-btn"+(share.status==="active"?" warn-btn":"")} style="flex:1;text-align:center" onClick=${onDisable}>${share.status==="active"?"🔴 Disable":"🟢 Enable"}</button>
        </div>
      </div>
    </div>`;
}

function FilesView({ isAdmin }) {
  const [path, setPath] = useState("/");
  const [entries, setEntries] = useState(null);
  const [err, setErr] = useState("");
  const [selected, setSelected] = useState({});
  const [progress, setProgress] = useState(null);
  const [dragging, setDragging] = useState(false);

  const load = useCallback((p) => {
    setSelected({});
    api.listFiles(p).then((e) => setEntries(e || [])).catch((e) => setErr(e.message));
  }, []);
  useEffect(() => { setErr(""); load(path); }, [path, load]);

  // Progress bar alimentata dagli eventi WebSocket file.progress.
  useEffect(() => {
    const ws = connectWS((type, payload) => {
      if (type !== "file.progress") return;
      if (payload.done) setProgress(null);
      else setProgress({ name: payload.name, bytes: payload.bytes || 0 });
    });
    return () => ws.close();
  }, []);

  const doUpload = async (files) => {
    if (!files || !files.length) return;
    setErr("");
    try { await uploadFiles(path, files); setProgress(null); load(path); }
    catch (e) { setErr(e.message); setProgress(null); }
  };

  const onDrop = (e) => {
    e.preventDefault(); setDragging(false);
    doUpload(e.dataTransfer.files);
  };

  const mkdir = async () => {
    const name = prompt(t("files.mkdirPrompt"));
    if (!name) return;
    try { await api.mkdir(joinPath(path, name)); load(path); } catch (e) { setErr(e.message); }
  };
  const rename = async (en) => {
    const name = prompt(t("files.renamePrompt"), en.name);
    if (!name || name === en.name) return;
    try { await api.renamePath(en.path, joinPath(parentPath(en.path), name)); load(path); } catch (e) { setErr(e.message); }
  };
  const chmod = async (en) => {
    const mode = prompt(t("files.chmodPrompt"), en.mode.replace(/[rwx-]/g, ""));
    if (!mode) return;
    try { await api.chmod(en.path, mode); load(path); } catch (e) { setErr(e.message); }
  };
  const delOne = async (en) => {
    if (!confirm(t("files.confirmDelete", { name: en.name }))) return;
    try { await api.removePath(en.path); load(path); } catch (e) { setErr(e.message); }
  };
  const delSelected = async () => {
    const paths = Object.keys(selected).filter((k) => selected[k]);
    if (!paths.length || !confirm(t("files.confirmDeleteN", { count: paths.length }))) return;
    try { for (const p of paths) await api.removePath(p); load(path); } catch (e) { setErr(e.message); }
  };

  const segments = path.split("/").filter(Boolean);
  const selCount = Object.values(selected).filter(Boolean).length;

  return html`
    <section>
      <header class="page-head">
        <h1>${t("files.title")}</h1>
        ${isAdmin && html`<div class="head-actions">
          ${selCount > 0 && html`<button class="link danger" onClick=${delSelected}>
            ${t("files.deleteSelected")} (${selCount})</button>`}
          <button class="link" onClick=${mkdir}>${t("files.newFolder")}</button>
          <label class="primary upload-btn">
            ${t("files.upload")}
            <input type="file" multiple style="display:none"
                   onChange=${(e) => doUpload(e.target.files)} />
          </label>
        </div>`}
      </header>

      <nav class="breadcrumb">
        <button class="link" onClick=${() => setPath("/")}>${t("files.root")}</button>
        ${segments.map((seg, i) => {
          const p = "/" + segments.slice(0, i + 1).join("/");
          return html`<span>/</span><button class="link" onClick=${() => setPath(p)}>${seg}</button>`;
        })}
      </nav>

      ${err && html`<p class="error">${err}</p>`}
      ${progress && html`<div class="progress"><span>${t("files.uploading", { name: progress.name })}</span>
        <span class="muted">${fmtBytes(progress.bytes)}</span></div>`}

      <div class=${dragging ? "dropzone dragging" : "dropzone"}
           onDragOver=${(e) => { e.preventDefault(); setDragging(true); }}
           onDragLeave=${() => setDragging(false)}
           onDrop=${onDrop}>
        ${entries === null && html`<p>${t("common.loading")}</p>`}
        ${entries && entries.length === 0 && html`<p class="muted">${t("files.empty")} · ${t("files.dropHint")}</p>`}
        ${entries && entries.length > 0 && html`
          <table class="data">
            <thead><tr>
              <th></th><th>${t("files.name")}</th><th>${t("files.size")}</th>
              <th>${t("files.permissions")}</th><th>${t("files.modified")}</th>
              ${isAdmin && html`<th>${t("files.actions")}</th>`}
            </tr></thead>
            <tbody>
              ${entries.map((en) => html`
                <tr>
                  <td><input type="checkbox" checked=${!!selected[en.path]}
                    onChange=${(e) => setSelected({ ...selected, [en.path]: e.target.checked })} /></td>
                  <td>${en.is_dir
                    ? html`<button class="link dir" onClick=${() => setPath(en.path)}>📁 ${en.name}</button>`
                    : html`<span>📄 ${en.name}</span>`}</td>
                  <td>${en.is_dir ? "—" : fmtBytes(en.size)}</td>
                  <td class="mono">${en.mode}</td>
                  <td class="muted">${fmtDate(en.mod_time)}</td>
                  ${isAdmin && html`<td class="row-actions">
                    ${!en.is_dir && html`<a class="link" href=${downloadUrl(en.path)}>${t("files.download")}</a>`}
                    <button class="link" onClick=${() => rename(en)}>${t("files.rename")}</button>
                    <button class="link" onClick=${() => chmod(en)}>${t("files.chmod")}</button>
                    <button class="link danger" onClick=${() => delOne(en)}>${t("files.delete")}</button>
                  </td>`}
                </tr>
              `)}
            </tbody>
          </table>
        `}
      </div>
    </section>
  `;
}

// QbtFolderPicker — file browser ristretto ai volumi dati (mai rootfs/etc/var).
function QbtFolderPicker({ start, onClose, onSelect }) {
  const [stack, setStack] = useState([start ? start : ""]);
  const cur = stack[stack.length - 1];
  const [entries, setEntries] = useState(null);
  const [err, setErr] = useState("");
  useEscClose(onClose);
  const load = useCallback((p) => {
    setErr(""); setEntries(null);
    api.qbtBrowse(p).then((e) => setEntries(e || [])).catch((e) => { setErr(e.message); setEntries([]); });
  }, []);
  useEffect(() => { load(cur); }, [cur, load]);
  const newFolder = async () => {
    if (cur === "") { setErr(t("qbt.enterVolumeFirst")); return; }
    const name = window.prompt(t("qbt.newFolderName"));
    if (!name) return;
    try { await api.qbtMkdir(cur, name); load(cur); } catch (e) { setErr(e.message); }
  };
  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card modal-lg" style="max-width:640px">
        <div class="modal-head"><h2>📁 ${t("qbt.chooseFolder")}</h2><button class="link" onClick=${onClose}>✕</button></div>
        <div class="info-box" style="margin-bottom:10px"><span class="mono">${cur === "" ? t("qbt.volumesRoot") : cur}</span></div>
        ${err && html`<p class="error">${err}</p>`}
        <div style="max-height:320px;overflow-y:auto;border:1px solid var(--border);border-radius:var(--radius);margin-bottom:12px">
          ${stack.length > 1 && html`<div class="disk-row" style="cursor:pointer" onClick=${() => setStack(stack.slice(0, -1))}><span>📁 ..</span></div>`}
          ${entries === null && html`<p class="muted" style="padding:10px">${t("common.loading")}</p>`}
          ${entries && entries.length === 0 && html`<p class="muted" style="padding:10px">${cur === "" ? t("qbt.noVolumes") : t("qbt.noSubfolders")}</p>`}
          ${entries && entries.map((e) => html`
            <div class="disk-row" style="cursor:pointer" onClick=${() => setStack([...stack, e.path])}><span>${cur === "" ? "💽" : "📁"} ${e.name}</span></div>`)}
        </div>
        <div class="form-actions" style="justify-content:space-between">
          <button class="action-btn" disabled=${cur === ""} onClick=${newFolder}>➕ ${t("qbt.newFolder")}</button>
          <div>
            <button class="link" onClick=${onClose}>${t("common.cancel")}</button>
            <button class="primary" disabled=${cur === ""} onClick=${() => onSelect(cur)}>${t("qbt.selectFolder")}</button>
          </div>
        </div>
      </div>
    </div>`;
}

// QbtWizard — configurazione guidata delle 3 directory + permessi + porta WebUI.
function QbtWizard({ onClose, onDone }) {
  const [vols, setVols] = useState(null);
  const [step, setStep] = useState(0);
  const [form, setForm] = useState({ temp_dir: "", torfile_dir: "", downloads_dir: "", perms_mode: "2770", webui_port: 8080 });
  const [picker, setPicker] = useState(null);
  const [val, setVal] = useState(null);
  const [applying, setApplying] = useState(false);
  const [result, setResult] = useState(null);
  const [err, setErr] = useState("");
  useEscClose(onClose);
  useEffect(() => {
    api.qbtVolumes().then((vs) => {
      setVols(vs || []);
      const base = vs && vs[0] ? vs[0].mount_point + "/qbittorrent" : "";
      if (base) setForm((f) => ({ ...f, temp_dir: base + "/temp", torfile_dir: base + "/torrents", downloads_dir: base + "/downloads" }));
    }).catch((e) => setErr(e.message));
  }, []);
  const set = (k, v) => setForm((f) => ({ ...f, [k]: v }));
  const gib = (n) => (n > 0 ? (n / 1073741824).toFixed(1) + " GiB" : "—");

  const dirFields = [
    ["temp_dir", "qbt.stepTemp", "qbt.stepTempDesc"],
    ["torfile_dir", "qbt.stepTorfile", "qbt.stepTorfileDesc"],
    ["downloads_dir", "qbt.stepDownloads", "qbt.stepDownloadsDesc"],
  ];

  const goValidate = async () => { setErr(""); try { setVal(await api.qbtValidate(form)); setStep(4); } catch (e) { setErr(e.message); } };
  const doApply = async () => {
    setApplying(true); setErr(""); setStep(5);
    try { setResult(await api.qbtConfigure(form)); } catch (e) { setErr(e.message); }
    finally { setApplying(false); }
  };

  const dirStep = (idx) => {
    const [key, tk, dk] = dirFields[idx];
    return html`
      <div>
        <p class="modal-section-title">${t(tk)}</p>
        <p class="muted" style="margin-bottom:12px">${t(dk)}</p>
        <div style="display:flex;gap:8px">
          <input readonly value=${form[key]} class="mono" style="flex:1" />
          <button class="action-btn" onClick=${() => setPicker(key)}>📁 ${t("qbt.browse")}</button>
        </div>
      </div>`;
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && !applying && onClose()}>
      <div class="modal-card modal-lg">
        <div class="modal-head"><h2>🧙 ${t("qbt.wizardTitle")} — ${step + 1}/6</h2><button class="link" disabled=${applying} onClick=${onClose}>✕</button></div>
        ${err && html`<p class="error">${err}</p>`}
        ${vols === null && html`<p class="muted">${t("common.loading")}</p>`}
        ${vols && vols.length === 0 && html`<p class="error">${t("qbt.noVolumes")}</p>`}
        ${vols && vols.length > 0 && html`
          ${step === 0 && html`
            <div>
              <p class="modal-section-title">${t("qbt.welcome")}</p>
              <p class="muted">${t("qbt.welcomeDesc")}</p>
              <ul class="muted" style="margin-top:8px;padding-left:18px">
                <li>${t("qbt.stepTemp")}</li><li>${t("qbt.stepTorfile")}</li><li>${t("qbt.stepDownloads")}</li>
              </ul>
            </div>`}
          ${step === 1 && dirStep(0)}
          ${step === 2 && dirStep(1)}
          ${step === 3 && dirStep(2)}
          ${step === 4 && html`
            <div>
              <p class="modal-section-title">${t("qbt.summary")}</p>
              <table class="data"><tbody>
                <tr><td>temp_dir</td><td class="mono">${form.temp_dir}</td><td class="muted">${val ? gib(val.free?.temp_dir) : ""}</td></tr>
                <tr><td>torfile_dir</td><td class="mono">${form.torfile_dir}</td><td class="muted">${val ? gib(val.free?.torfile_dir) : ""}</td></tr>
                <tr><td>downloads_dir</td><td class="mono">${form.downloads_dir}</td><td class="muted">${val ? gib(val.free?.downloads_dir) : ""}</td></tr>
              </tbody></table>
              <div class="form-grid-3" style="margin-top:12px">
                <label>${t("qbt.perms")}<select value=${form.perms_mode} onChange=${(e) => set("perms_mode", e.target.value)}>
                  <option value="2770">2770 — ${t("qbt.permsPrivate")}</option>
                  <option value="2775">2775 — ${t("qbt.permsShared")}</option>
                </select></label>
                <label>${t("qbt.webuiPort")}<input type="number" value=${form.webui_port} onInput=${(e) => set("webui_port", +e.target.value)} /></label>
              </div>
              ${val && val.errors?.length > 0 && html`<div class="error-box" style="margin-top:12px">${val.errors.map((x) => html`<div>⛔ ${x}</div>`)}</div>`}
              ${val && val.warnings?.length > 0 && html`<div class="warn-box" style="margin-top:8px">${val.warnings.map((x) => html`<div>⚠️ ${x}</div>`)}</div>`}
              ${val && val.ok && html`<p class="success-box" style="margin-top:12px">✅ ${t("qbt.validationOk")}</p>`}
            </div>`}
          ${step === 5 && html`
            <div>
              <p class="modal-section-title">${t("qbt.applying")}</p>
              ${applying && html`<p class="muted">${t("qbt.applyingWait")}</p>`}
              ${result && result.steps && html`<div class="table-wrap"><table class="data"><tbody>
                ${result.steps.map((s) => html`<tr><td>${s.ok ? "✅" : "⛔"}</td><td class="mono">${s.name}</td><td class="muted">${s.msg || ""}</td></tr>`)}
              </tbody></table></div>`}
              ${result && result.webui_password && html`
                <div class="success-box" style="margin-top:12px">
                  ✅ ${t("qbt.done")}<br/>
                  WebUI: <span class="mono">http://${window.location.hostname}:${result.webui_port}</span><br/>
                  ${t("qbt.credentials")}: <span class="mono">admin / ${result.webui_password}</span>
                  <div class="muted" style="font-size:12px;margin-top:4px">${t("qbt.savePassword")}</div>
                </div>`}
            </div>`}
        `}
        <div class="form-actions" style="margin-top:18px">
          ${step > 0 && step < 5 && html`<button class="link" onClick=${() => setStep(step - 1)}>${t("qbt.back")}</button>`}
          ${step < 3 && html`<button class="primary" disabled=${!vols || vols.length === 0} onClick=${() => setStep(step + 1)}>${t("qbt.next")}</button>`}
          ${step === 3 && html`<button class="primary" onClick=${goValidate}>${t("qbt.next")}</button>`}
          ${step === 4 && html`<button class="primary" style="background:#10b981" disabled=${val && !val.ok} onClick=${doApply}>${t("qbt.apply")}</button>`}
          ${step === 5 && !applying && html`<button class="primary" onClick=${() => { onDone && onDone(); onClose(); }}>${t("qbt.finish")}</button>`}
        </div>
        ${picker && html`<${QbtFolderPicker} start=${form[picker]} onClose=${() => setPicker(null)}
          onSelect=${(p) => { set(picker, p); setPicker(null); }} />`}
      </div>
    </div>`;
}

// QbtView — pagina app qBittorrent (stato + wizard + gestione servizio).
function QbtView({ isAdmin, qbt, onChanged }) {
  const [wizard, setWizard] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const state = qbt?.state || "unavailable";
  const cfg = qbt?.config || {};
  const port = qbt?.webui_port || 8080;
  const badgeCls = state === "running" ? "active" : (state === "unavailable" ? "degraded" : "");
  const act = async (fn) => { setBusy(true); setErr(""); try { await fn(); onChanged && onChanged(); } catch (e) { setErr(e.message); } finally { setBusy(false); } };
  return html`
    <section>
      <header class="page-head">
        <h1>qBittorrent</h1>
        <span class=${"badge " + badgeCls}>${t("qbt.state." + state)}</span>
      </header>
      ${err && html`<p class="error">${err}</p>`}

      ${state === "unavailable" && html`<div class="info-box" style="margin-top:12px">⚠️ ${t("qbt.tooltipUnavailable")}</div>`}

      ${state === "available" && html`
        <div class="form-card" style="margin-top:12px">
          <p class="muted">${t("qbt.introConfigure")}</p>
          <div class="form-actions">
            <button class="primary" disabled=${!isAdmin} onClick=${() => setWizard(true)}>${t("qbt.configure")}</button>
          </div>
        </div>`}

      ${(state === "running" || state === "stopped" || state === "configured") && html`
        <div class="form-card" style="margin-top:12px">
          <p style="margin-bottom:10px">
            WebUI: <a href=${"http://" + window.location.hostname + ":" + port} target="_blank" class="mono">http://${window.location.hostname}:${port}</a>
          </p>
          <div class="stats-row">
            <div class="stat-card"><div><span class="stat-label">temp_dir</span><span class="mono" style="display:block">${cfg.temp_dir || "—"}</span></div></div>
            <div class="stat-card"><div><span class="stat-label">torfile_dir</span><span class="mono" style="display:block">${cfg.torfile_dir || "—"}</span></div></div>
            <div class="stat-card"><div><span class="stat-label">downloads_dir</span><span class="mono" style="display:block">${cfg.downloads_dir || "—"}</span></div></div>
          </div>
          ${isAdmin && html`<div class="form-actions" style="margin-top:14px">
            ${state === "running"
              ? html`<button class="action-btn danger-btn" disabled=${busy} onClick=${() => act(api.qbtStop)}>⏹ ${t("qbt.stop")}</button>`
              : html`<button class="action-btn" disabled=${busy} onClick=${() => act(api.qbtStart)}>▶ ${t("qbt.start")}</button>`}
            <button class="action-btn" disabled=${busy} onClick=${() => setWizard(true)}>⚙ ${t("qbt.reconfigure")}</button>
          </div>`}
        </div>`}

      ${wizard && html`<${QbtWizard} onClose=${() => setWizard(false)} onDone=${onChanged} />`}
    </section>
  `;
}

// AdminView — pannello amministrazione: spegnimento/riavvio + visualizzatore log.
function AdminView({ isAdmin }) {
  const [confirm, setConfirm] = useState(null); // "reboot" | "shutdown" | null
  const [notice, setNotice] = useState("");
  const [unit, setUnit] = useState("");
  const [lines, setLines] = useState(300);
  const [units, setUnits] = useState([]);
  const [entries, setEntries] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    setErr(""); setEntries(null);
    api.adminLogs(unit, lines)
      .then((r) => { setUnits(r.units || []); setEntries(r.entries || []); })
      .catch((e) => { setErr(e.message); setEntries([]); });
  }, [unit, lines]);
  useEffect(() => { load(); }, [load]);

  const doPower = async (action) => {
    setBusy(true); setErr("");
    try {
      if (action === "reboot") await api.adminReboot(); else await api.adminShutdown();
      setNotice(action === "reboot" ? t("admin.rebooting") : t("admin.shuttingDown"));
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); setConfirm(null); }
  };

  return html`
    <section>
      <header class="page-head"><h1>${t("nav.admin")}</h1></header>
      ${err && html`<p class="error">${err}</p>`}
      ${notice && html`<div class="info-box" style="margin-top:12px">⏳ ${notice}</div>`}

      <div class="form-card" style="margin-top:12px">
        <h2 class="section-title" style="margin-top:0">${t("admin.power")}</h2>
        <p class="muted">${t("admin.powerHint")}</p>
        <div class="form-actions">
          <button class="action-btn" disabled=${!isAdmin || busy} onClick=${() => setConfirm("reboot")}>🔄 ${t("admin.reboot")}</button>
          <button class="action-btn danger-btn" disabled=${!isAdmin || busy} onClick=${() => setConfirm("shutdown")}>⏻ ${t("admin.shutdown")}</button>
        </div>
      </div>

      <div class="form-card" style="margin-top:16px">
        <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:10px">
          <h2 class="section-title" style="margin:0">${t("admin.logs")}</h2>
          <div style="display:flex;gap:8px;align-items:center">
            <select value=${unit} onChange=${(e) => setUnit(e.target.value)}>
              <option value="">${t("admin.allUnits")}</option>
              ${units.map((u) => html`<option value=${u}>${u}</option>`)}
            </select>
            <select value=${lines} onChange=${(e) => setLines(Number(e.target.value))}>
              ${[100, 300, 500, 1000].map((n) => html`<option value=${n}>${n}</option>`)}
            </select>
            <button class="action-btn" disabled=${busy} onClick=${load}>↻ ${t("admin.refresh")}</button>
          </div>
        </div>
        ${entries === null && html`<p class="muted">${t("common.loading")}</p>`}
        ${entries && entries.length === 0 && html`<p class="muted">${t("admin.noLogs")}</p>`}
        ${entries && entries.length > 0 && html`
          <div class="log-view">
            ${entries.map((e) => html`
              <div class=${"log-line log-" + e.level}>
                <span class="log-time">${e.time}</span>
                ${e.unit && html`<span class="log-unit">${e.unit}</span>`}
                <span class="log-msg">${e.message}</span>
              </div>`)}
          </div>`}
      </div>

      ${confirm && html`
        <div class="modal-overlay" onClick=${(ev) => ev.target === ev.currentTarget && !busy && setConfirm(null)}>
          <div class="modal-card">
            <div class="modal-head">
              <h2>${confirm === "reboot" ? "🔄 " + t("admin.reboot") : "⏻ " + t("admin.shutdown")}</h2>
              <button class="link" disabled=${busy} onClick=${() => setConfirm(null)}>✕</button>
            </div>
            <p class="warn-box" style="margin-top:4px">⚠️ ${confirm === "reboot" ? t("admin.confirmReboot") : t("admin.confirmShutdown")}</p>
            <div class="form-actions">
              <button class="link" disabled=${busy} onClick=${() => setConfirm(null)}>${t("common.cancel")}</button>
              <button class="primary" style="background:var(--danger)" disabled=${busy} onClick=${() => doPower(confirm)}>
                ${busy ? "…" : (confirm === "reboot" ? t("admin.reboot") : t("admin.shutdown"))}
              </button>
            </div>
          </div>
        </div>`}
    </section>
  `;
}

// MediaLibrary mostra i file condivisi via DLNA raggruppati per categoria.
function MediaLibrary() {
  const [files, setFiles] = useState(null);
  const [tab, setTab] = useState("V");
  const [err, setErr] = useState("");
  const load = useCallback(() => {
    setErr("");
    api.listMediaFiles().then((f) => setFiles(f || { V: [], P: [], A: [] })).catch((e) => setErr(e.message));
  }, []);
  useEffect(() => { load(); }, [load]);
  const cats = [["V", "🎬 Video"], ["P", "🖼️ Pictures"], ["A", "🎵 Music"]];
  const list = files ? (files[tab] || []) : [];
  return html`
    <div style="margin-top:32px;border-top:1px solid var(--border);padding-top:24px">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:14px">
        <h2 class="section-title" style="margin:0">Media Library</h2>
        <button class="action-btn" onClick=${load}>↻ Refresh</button>
      </div>
      ${err && html`<p class="error">${err}</p>`}
      <div class="panel-tabs" style="margin-bottom:14px">
        ${cats.map(([k, label]) => html`
          <button class=${"panel-tab" + (tab === k ? " active" : "")} onClick=${() => setTab(k)}>
            ${label}${files ? ` (${(files[k] || []).length})` : ""}
          </button>`)}
      </div>
      ${files === null && html`<p class="muted">${t("common.loading")}</p>`}
      ${files && list.length === 0 && html`<p class="muted">No files in this category. Add a media folder above, then Rebuild catalog.</p>`}
      ${files && list.length > 0 && html`
        <div class="table-wrap"><table class="data">
          <thead><tr><th>Name</th><th>Size</th><th>Path</th></tr></thead>
          <tbody>
            ${list.map((f) => html`
              <tr>
                <td class="mono">${f.name}</td>
                <td>${fmtBytes(f.size)}</td>
                <td class="muted" style="font-size:12px">${f.path}</td>
              </tr>`)}
          </tbody>
        </table></div>`}
    </div>`;
}

// FolderPicker naviga le cartelle sotto la root del file manager (/srv) e
// restituisce il percorso assoluto scelto. Mostra solo directory.
function FolderPicker({ root = "/srv", onClose, onSelect }) {
  const [rel, setRel] = useState("");
  const [entries, setEntries] = useState(null);
  const [err, setErr] = useState("");
  useEscClose(onClose);
  const abs = root + (rel ? "/" + rel : "");

  const load = useCallback((r) => {
    setErr(""); setEntries(null);
    api.listFiles(r).then((list) => setEntries((list || []).filter((e) => e.is_dir)))
      .catch((e) => { setErr(e.message); setEntries([]); });
  }, []);
  useEffect(() => { load(rel); }, [rel, load]);

  const enter = (name) => setRel(rel ? rel + "/" + name : name);
  const up = () => setRel(rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : "");
  const newFolder = async () => {
    const name = window.prompt("New folder name:");
    if (!name) return;
    try { await api.mkdir(rel ? rel + "/" + name : name); load(rel); }
    catch (e) { setErr(e.message); }
  };

  return html`
    <div class="modal-overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="modal-card modal-lg" style="max-width:640px">
        <div class="modal-head"><h2>📁 Choose Folder</h2><button class="link" onClick=${onClose}>✕</button></div>
        <div class="info-box" style="margin-bottom:10px"><span class="mono">${abs}</span></div>
        ${err && html`<p class="error">${err}</p>`}
        <div style="max-height:320px;overflow-y:auto;border:1px solid var(--border);border-radius:var(--radius);margin-bottom:12px">
          ${rel !== "" && html`<div class="disk-row" style="cursor:pointer" onClick=${up}><span>📁 ..</span></div>`}
          ${entries === null && html`<p class="muted" style="padding:10px">${t("common.loading")}</p>`}
          ${entries && entries.length === 0 && html`<p class="muted" style="padding:10px">No subfolders here.</p>`}
          ${entries && entries.map((e) => html`
            <div class="disk-row" style="cursor:pointer" onClick=${() => enter(e.name)}><span>📁 ${e.name}</span></div>
          `)}
        </div>
        <div class="form-actions" style="justify-content:space-between">
          <button class="action-btn" onClick=${newFolder}>➕ New Folder</button>
          <div>
            <button class="link" onClick=${onClose}>${t("common.cancel")}</button>
            <button class="primary" onClick=${() => onSelect(abs)}>Select this folder</button>
          </div>
        </div>
      </div>
    </div>`;
}

function DlnaView({ isAdmin }) {
  const [dirs, setDirs] = useState(null);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [form, setForm] = useState({ path: "", type: "" });
  const [picker, setPicker] = useState(false);

  const load = useCallback(() => {
    api.listMediaDirs().then((d) => setDirs(d || [])).catch((e) => setErr(e.message));
  }, []);
  useEffect(() => { load(); }, [load]);

  const add = async () => {
    setErr("");
    if (!form.path) return;
    try { await api.addMediaDir(form); setForm({ path: "", type: "" }); load(); }
    catch (e) { setErr(e.message); }
  };
  const remove = async (path) => {
    try { await api.removeMediaDir(path); load(); } catch (e) { setErr(e.message); }
  };
  const rescan = async () => {
    setErr(""); setMsg("");
    try { await api.dlnaRescan(); setMsg(t("dlna.rescan") + " ✓"); } catch (e) { setErr(e.message); }
  };

  return html`
    <section>
      <header class="page-head">
        <h1>${t("dlna.title")}</h1>
        ${isAdmin && html`<button class="primary" onClick=${rescan}>${t("dlna.rescan")}</button>`}
      </header>
      ${err && html`<p class="error">${err}</p>`}
      ${msg && html`<p class="muted">${msg}</p>`}

      ${isAdmin && html`
        <div class="form-card">
          <div class="form-grid">
            <label>${t("dlna.path")}
              <div style="display:flex;gap:8px">
                <input value=${form.path} placeholder="/srv/media/…" style="flex:1"
                  onInput=${(e) => setForm({ ...form, path: e.target.value })} />
                <button type="button" class="action-btn" onClick=${() => setPicker(true)}>📁 Browse</button>
              </div>
            </label>
            <label>${t("dlna.type")}
              <select value=${form.type} onChange=${(e) => setForm({ ...form, type: e.target.value })}>
                <option value="">${t("dlna.types.")}</option>
                <option value="A">${t("dlna.types.A")}</option>
                <option value="V">${t("dlna.types.V")}</option>
                <option value="P">${t("dlna.types.P")}</option>
              </select></label>
          </div>
          <div class="form-actions">
            <button class="primary" onClick=${add}>${t("dlna.add")}</button>
          </div>
        </div>
      `}
      ${picker && html`<${FolderPicker}
        onClose=${() => setPicker(false)}
        onSelect=${(p) => { setForm((f) => ({ ...f, path: p })); setPicker(false); }} />`}

      ${dirs === null && html`<p>${t("common.loading")}</p>`}
      ${dirs && dirs.length === 0 && html`<p class="muted">${t("dlna.empty")}</p>`}
      ${dirs && dirs.length > 0 && html`
        <table class="data">
          <thead><tr>
            <th>${t("dlna.path")}</th><th>${t("dlna.type")}</th>${isAdmin && html`<th></th>`}
          </tr></thead>
          <tbody>
            ${dirs.map((d) => html`
              <tr>
                <td class="mono">${d.path}</td>
                <td>${t(`dlna.types.${d.type}`)}</td>
                ${isAdmin && html`<td>
                  <button class="link danger" onClick=${() => remove(d.path)}>${t("dlna.remove")}</button>
                </td>`}
              </tr>
            `)}
          </tbody>
        </table>
      `}

      <${MediaLibrary} />
    </section>
  `;
}

function Placeholder({ title }) {
  return html`<section><h1>${title}</h1><p class="muted">In sviluppo / Under development</p></section>`;
}

render(html`<${App} />`, document.getElementById("app"));
