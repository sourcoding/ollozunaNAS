// Client API: wrapper su fetch che gestisce automaticamente il token CSRF per
// le richieste mutanti e centralizza la gestione degli errori e del 401.
let csrfToken = null;

export function setCsrfToken(token) {
  csrfToken = token;
}

async function request(method, path, body) {
  const headers = { "Content-Type": "application/json" };
  if (csrfToken && method !== "GET") {
    headers["X-CSRF-Token"] = csrfToken;
  }
  const res = await fetch(`/api${path}`, {
    method,
    headers,
    credentials: "same-origin",
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 401) {
    window.dispatchEvent(new CustomEvent("session-expired"));
    throw new Error("unauthorized");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || `http_${res.status}`);
  }
  return data;
}

export const api = {
  // Auth
  login: (username, password) => request("POST", "/auth/login", { username, password }),
  logout: () => request("POST", "/auth/logout"),
  me: () => request("GET", "/auth/me"),

  // Users
  listUsers: () => request("GET", "/users/"),
  createUser: (u) => request("POST", "/users/", u),
  deleteUser: (name) => request("DELETE", `/users/${encodeURIComponent(name)}`),
  setPassword: (name, password) => request("POST", `/users/${encodeURIComponent(name)}/password`, { password }),
  listGroups: () => request("GET", "/groups"),

  // RAID
  listArrays: () => request("GET", "/raid/arrays"),
  listDisks: () => request("GET", "/raid/disks"),
  createArray: (a) => request("POST", "/raid/arrays", a),
  deleteArray: (md) => request("DELETE", `/raid/arrays/${encodeURIComponent(md.replace(/^\/dev\//, ""))}?confirm=true`),
  listFilesystems: () => request("GET", "/raid/filesystems"),
  createFilesystem: (md, fstype, mountPoint) => request("POST", `/raid/arrays/${encodeURIComponent(md.replace(/^\/dev\//, ""))}/filesystem`, { fstype, mount_point: mountPoint, confirm: true }),
  mountFilesystem: (md, fstype, mountPoint) => request("POST", `/raid/arrays/${encodeURIComponent(md.replace(/^\/dev\//, ""))}/filesystem/mount`, { fstype, mount_point: mountPoint, confirm: true }),
  unmountFilesystem: (md, mountPoint) => request("POST", `/raid/arrays/${encodeURIComponent(md.replace(/^\/dev\//, ""))}/filesystem/unmount`, { mount_point: mountPoint, confirm: true }),
  growFilesystem: (md, fstype) => request("POST", `/raid/arrays/${encodeURIComponent(md.replace(/^\/dev\//, ""))}/filesystem/grow`, { fstype }),
  deleteFilesystem: (md) => request("DELETE", `/raid/arrays/${encodeURIComponent(md.replace(/^\/dev\//, ""))}/filesystem?confirm=true`),
  smart: (device) => request("GET", `/raid/disks/${encodeURIComponent(device)}/smart`),
  wipeDisk: (device) => request("POST", `/raid/disks/${encodeURIComponent(device.replace(/^\/dev\//, ""))}/wipe`, { confirm: true }),
  addDisk: (md, disk) => request("POST", `/raid/arrays/${encodeURIComponent(md)}/disks`, { disk }),
  removeDisk: (md, disk) =>
    request("DELETE", `/raid/arrays/${encodeURIComponent(md)}/disks/${encodeURIComponent(disk)}`),

  // Shares
  listShares: () => request("GET", "/shares/"),
  createShare: (s) => request("POST", "/shares/", s),
  updateShare: (id, s) => request("PUT", `/shares/${id}`, s),
  deleteShare: (id) => request("DELETE", `/shares/${id}?confirm=true`),

  // File manager
  listFiles: (path) => request("GET", `/files/?path=${encodeURIComponent(path)}`),
  mkdir: (path) => request("POST", "/files/mkdir", { path }),
  renamePath: (src, dst) => request("POST", "/files/rename", { src, dst }),
  chmod: (path, mode) => request("POST", "/files/chmod", { path, mode }),
  removePath: (path) => request("DELETE", `/files/?path=${encodeURIComponent(path)}&confirm=true`),

  // DLNA
  listMediaDirs: () => request("GET", "/dlna/dirs"),
  listMediaFiles: () => request("GET", "/dlna/files"),

  // qBittorrent app
  qbtStatus: () => request("GET", "/apps/qbt/status"),
  qbtVolumes: () => request("GET", "/apps/qbt/volumes"),
  qbtBrowse: (path) => request("GET", `/apps/qbt/browse?path=${encodeURIComponent(path || "")}`),
  qbtMkdir: (parent, name) => request("POST", "/apps/qbt/mkdir", { parent, name }),
  qbtValidate: (cfg) => request("POST", "/apps/qbt/validate", cfg),
  qbtConfigure: (cfg) => request("POST", "/apps/qbt/configure", cfg),
  qbtStart: () => request("POST", "/apps/qbt/start", {}),
  qbtStop: () => request("POST", "/apps/qbt/stop", {}),
  adminLogs: (unit, lines) => request("GET", `/admin/logs?unit=${encodeURIComponent(unit || "")}&lines=${lines || 300}`),
  adminReboot: () => request("POST", "/admin/reboot", {}),
  adminShutdown: () => request("POST", "/admin/shutdown", {}),
  addMediaDir: (d) => request("POST", "/dlna/dirs", d),
  removeMediaDir: (path) => request("DELETE", `/dlna/dirs?path=${encodeURIComponent(path)}`),
  dlnaRescan: () => request("POST", "/dlna/rescan"),
};

// uploadFiles invia uno o più file via multipart. Non usa request() perché il
// body è FormData (non JSON); aggiunge a mano il token CSRF. La progress bar è
// alimentata dagli eventi WebSocket file.progress emessi dal backend.
export async function uploadFiles(dirPath, files) {
  const fd = new FormData();
  for (const f of files) fd.append("files", f, f.name);
  const headers = {};
  if (csrfToken) headers["X-CSRF-Token"] = csrfToken;
  const res = await fetch(`/api/files/upload?path=${encodeURIComponent(dirPath)}`, {
    method: "POST",
    headers,
    credentials: "same-origin",
    body: fd,
  });
  if (res.status === 401) {
    window.dispatchEvent(new CustomEvent("session-expired"));
    throw new Error("unauthorized");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `http_${res.status}`);
  return data;
}

// downloadUrl restituisce l'href per scaricare un file (link diretto, GET).
export function downloadUrl(path) {
  return `/api/files/download?path=${encodeURIComponent(path)}`;
}

// connectWS apre il canale eventi in tempo reale e invoca onEvent(type, payload).
export function connectWS(onEvent) {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  ws.onmessage = (ev) => {
    try {
      const { type, payload } = JSON.parse(ev.data);
      onEvent(type, payload);
    } catch (_) {}
  };
  ws.onclose = () => setTimeout(() => connectWS(onEvent), 3000); // reconnect
  return ws;
}
