"use strict";

/* ======================================================================
   DOpad Panel — local control surface
   The page is served by the panel on 127.0.0.1 only. It never talks to
   anything outside this computer.
   ====================================================================== */

const TOKEN = window.DOPAD_TOKEN || "";
const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

const el = {
  connDot: $("#conn-dot"),
  connText: $("#conn-text"),
  tabs: $$(".tab"),
  pages: { server: $("#page-server"), editor: $("#page-editor") },
  quit: $("#quit-panel"),

  modes: $$(".mode"),
  modeBadge: $("#mode-badge"),
  selfNote: $("#self-note"),
  binary: $("#binary"),
  port: $("#port"),
  password: $("#password"),
  passwordRow: $("#password-row"),
  detect: $("#detect"),
  detected: $("#detected"),
  start: $("#start"),
  stop: $("#stop"),
  buildHint: $("#build-hint"),
  refreshState: $("#refresh-state"),

  statusPill: $("#status-pill"),
  uptime: $("#uptime"),
  factPid: $("#fact-pid"),
  factExit: $("#fact-exit"),
  url: $("#url"),
  copyUrl: $("#copy-url"),
  openUrl: $("#open-url"),
  statusNote: $("#status-note"),
  qrImg: $("#qr-img"),
  qrEmpty: $("#qr-empty"),
  qrNote: $("#qr-note"),
  refreshQr: $("#refresh-qr"),

  autoscroll: $("#autoscroll"),
  clearLog: $("#clear-log"),
  log: $("#log"),

  refreshCatalog: $("#refresh-catalog"),
  typeList: $("#type-list"),
  buttonGrid: $("#button-grid"),
  triggerGrid: $("#trigger-grid"),
  catalogNote: $("#catalog-note"),

  layoutSelect: $("#layout-select"),
  loadLayout: $("#load-layout"),
  newLayout: $("#new-layout"),
  layoutId: $("#layout-id"),
  layoutName: $("#layout-name"),
  saveLayout: $("#save-layout"),
  importBtn: $("#import"),
  exportBtn: $("#export"),
  copyJson: $("#copy-json"),
  dirty: $("#dirty"),
  importFile: $("#import-file"),

  stageWrap: $("#stage-wrap"),
  stage: $("#stage"),
  stageElements: $("#stage-elements"),
  selection: $("#selection"),
  handle: $("#selection-handle"),
  stageInfo: $("#stage-info"),

  selType: $("#sel-type"),
  selBadge: $("#sel-badge"),
  inspectorEmpty: $("#inspector-empty"),
  inspectorFields: $("#inspector-fields"),
  partsSection: $("#parts-section"),
  partsList: $("#parts-list"),
  partFields: $("#part-fields"),
  addPartButton: $("#add-part-button"),
  addPartTrigger: $("#add-part-trigger"),
  deletePart: $("#delete-part"),
  layers: $("#layers"),
  dup: $("#dup"),
  raise: $("#raise"),
  lower: $("#lower"),
  del: $("#del"),

  toasts: $("#toasts"),
};

let state = null;
let settings = { mode: "wifi", port: "8080", binary: "" };
let lastSeq = 0;
let activeTab = "server";
let logLines = 0;

let qrObjectURL = "";
let qrKey = "";
let qrBusy = false;

let catalog = null;
let layouts = [];
let doc = { name: "تخطيطي", elements: [] };
let layoutId = "my-layout";
let selected = -1;
let selectedPart = -1;
let dirty = false;
let editorReady = false;

let stageW = 1000;
let stageH = 562.5;
let stageU = 5.5;
let drag = null;
let resize = null;

let settingsTimer = 0;
let settingsPending = false;
let settingsVersion = 0;

/* --------------------------------------------------------------- helpers */

function toast(message, bad = false) {
  const node = document.createElement("div");
  node.className = "toast" + (bad ? " bad" : "");
  node.textContent = message;
  el.toasts.appendChild(node);
  setTimeout(() => {
    node.style.opacity = "0";
    node.style.transition = "opacity .25s ease";
    setTimeout(() => node.remove(), 260);
  }, 4200);
}

async function api(path, options = {}) {
  const opts = { ...options, headers: { ...(options.headers || {}), "X-DOpad-Token": TOKEN } };
  if (options.body !== undefined && typeof options.body !== "string") {
    opts.body = JSON.stringify(options.body);
    opts.headers["Content-Type"] = "application/json";
  }
  const response = await fetch(path, opts);
  const text = await response.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!response.ok) throw new Error(data.error || response.statusText || "خطأ غير معروف");
  return data;
}

function setConn(ok, text) {
  el.connDot.classList.toggle("ok", ok === true);
  el.connDot.classList.toggle("bad", ok === false);
  el.connText.textContent = text;
}

function deepCopy(value) {
  return JSON.parse(JSON.stringify(value));
}

function round1(value) {
  return Math.round(value * 2) / 2;
}

function fmtMode(mode) {
  return mode === "bt" ? "Bluetooth" : mode === "self" ? "Self" : "Wi‑Fi";
}

function fmtDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return "";
  const total = Math.floor(ms / 1000);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return (hours > 0 ? hours + ":" : "") + pad(minutes) + ":" + pad(seconds);
}

/* --------------------------------------------------------------- tabs */

function switchTab(name) {
  activeTab = name;
  for (const tab of el.tabs) tab.classList.toggle("active", tab.dataset.tab === name);
  for (const [key, page] of Object.entries(el.pages)) page.classList.toggle("active", key === name);
  if (name === "editor") {
    requestAnimationFrame(() => {
      resizeStage();
      renderStage();
    });
  }
}

for (const tab of el.tabs) tab.addEventListener("click", () => switchTab(tab.dataset.tab));

/* --------------------------------------------------------------- server */

function renderState() {
  if (!state) return;
  if (!settingsPending) settings = state.settings || settings;

  const running = !!state.running;
  const starting = !!state.starting;
  const mode = running ? state.mode : settings.mode;

  for (const card of el.modes) {
    card.classList.toggle("active", card.dataset.mode === mode);
    card.disabled = running;
  }
  el.modeBadge.textContent = fmtMode(mode);

  if (!settingsPending && document.activeElement !== el.binary) el.binary.value = settings.binary || "";
  if (!settingsPending && document.activeElement !== el.port) el.port.value = settings.port || "";

  el.detected.textContent = state.detectedBinary
    ? "المكتشف: " + state.detectedBinary
    : "لم يُعثر على DOpad. ابنِه بالأمر make build أو اكتب مساره.";

  const selfMode = mode === "self";
  const needsPassword = selfMode && state.platform === "linux" && !state.isRoot;
  el.passwordRow.classList.toggle("hidden", !needsPassword);
  el.selfNote.classList.toggle("hidden", !selfMode);
  if (selfMode) {
    if (state.platform !== "linux") {
      el.selfNote.textContent = "وضع Self متاح على لينكس فقط. استخدم Wi‑Fi أو Bluetooth على هذا النظام.";
    } else if (state.isRoot) {
      el.selfNote.textContent = "اللوحة تعمل بصلاحيات root، لذا سيعمل وضع Self مباشرة بلا كلمة مرور.";
    } else if (state.hasPkexec) {
      el.selfNote.textContent = "سيطلب النظام تأكيدًا رسوميًا (pkexec) عند التشغيل. يمكنك بدلًا من ذلك إدخال كلمة مرور sudo أدناه.";
    } else if (state.hasSudo) {
      el.selfNote.textContent = "أدخل كلمة مرور sudo، أو شغّل اللوحة بالأمر sudo ./dopad-panel.";
    } else {
      el.selfNote.textContent = "وضع Self يحتاج صلاحيات root. شغّل اللوحة بالأمر sudo ./dopad-panel.";
    }
  }

  const missingBinary = !state.detectedBinary && !el.binary.value.trim();
  el.buildHint.classList.toggle("hidden", !missingBinary);
  if (missingBinary) {
    el.buildHint.textContent = "لم أجد ملف DOpad التنفيذي. ابنِ الخادم بالأمر make build، أو حدّد مساره في الحقل أعلاه.";
  }

  el.start.disabled = running || missingBinary;
  el.stop.disabled = !running;

  el.statusPill.textContent = running ? (starting ? "جارٍ التشغيل…" : "يعمل") : "متوقف";
  el.statusPill.classList.toggle("ok", running && !starting);
  el.statusPill.classList.toggle("busy", running && starting);

  el.factPid.textContent = state.pid ? String(state.pid) : "—";
  el.factExit.textContent = running ? "—" : String(state.exitCode ?? 0);

  el.url.value = state.url || "";
  el.copyUrl.disabled = !state.url;
  el.openUrl.disabled = !state.url;

  if (starting) {
    el.statusNote.textContent = "الخادم يبدأ الآن… قد يظهر تأكيد النظام في وضع Self.";
  } else if (running) {
    el.statusNote.textContent = "الخادم يعمل. افتح الرابط على الهاتف المتصل بالشبكة نفسها.";
  } else if (state.exitCode && state.exitCode !== 0) {
    el.statusNote.textContent = "توقف الخادم برمز الخروج " + state.exitCode + ". راجع السجل.";
  } else {
    el.statusNote.textContent = "";
  }

  updateUptime();
  refreshQR();
}

function updateUptime() {
  if (state && state.running && state.startedAt) {
    el.uptime.textContent = "منذ " + fmtDuration(Date.now() - Date.parse(state.startedAt));
  } else {
    el.uptime.textContent = "";
  }
}

function clearQR(message) {
  if (qrObjectURL) {
    URL.revokeObjectURL(qrObjectURL);
    qrObjectURL = "";
  }
  el.qrImg.hidden = true;
  el.qrImg.removeAttribute("src");
  el.qrEmpty.hidden = false;
  el.qrEmpty.textContent = message;
  el.qrNote.textContent = "";
}

async function refreshQR(force = false) {
  if (!state || !state.running || !state.port) {
    qrKey = "";
    clearQR("شغّل الخادم لعرض رمز QR.");
    return;
  }

  const key = state.port + "|" + (state.url || "");
  if (!force && key === qrKey) return;
  if (qrBusy) return;

  qrKey = key;
  qrBusy = true;
  try {
    const response = await fetch("/api/qr?t=" + Date.now(), {
      headers: { "X-DOpad-Token": TOKEN },
    });
    if (!response.ok) {
      let message = "لا يوجد رمز QR بعد.";
      try {
        const data = await response.json();
        if (data && data.error) message = data.error;
      } catch {
        /* response is not JSON */
      }
      clearQR(message);
      return;
    }
    const blob = await response.blob();
    if (qrObjectURL) URL.revokeObjectURL(qrObjectURL);
    qrObjectURL = URL.createObjectURL(blob);
    el.qrImg.src = qrObjectURL;
    el.qrImg.hidden = false;
    el.qrEmpty.hidden = true;
    el.qrNote.textContent = state.url ? "الرابط: " + state.url : "";
  } catch {
    clearQR("تعذّر جلب رمز QR من الخادم.");
  } finally {
    qrBusy = false;
  }
}

async function refreshState() {
  try {
    state = await api("/api/state");
    if (!settingsPending) settings = state.settings || settings;
    renderState();
    setConn(true, "متصل");
  } catch {
    setConn(false, "انقطع الاتصال");
  }
}

function saveSettingsSoon() {
  settingsPending = true;
  settingsVersion += 1;
  const version = settingsVersion;
  clearTimeout(settingsTimer);
  settingsTimer = setTimeout(async () => {
    try {
      const saved = { ...settings };
      state = await api("/api/settings", { method: "POST", body: saved });
      if (version !== settingsVersion) return;
      settingsPending = false;
      settings = state.settings || saved;
      renderState();
    } catch (error) {
      if (version !== settingsVersion) return;
      settingsPending = false;
      toast(error.message, true);
      refreshState();
    }
  }, 350);
}

for (const card of el.modes) {
  card.addEventListener("click", () => {
    if (state && state.running) return;
    settings.mode = card.dataset.mode;
    renderState();
    saveSettingsSoon();
  });
}

el.binary.addEventListener("input", () => {
  settings.binary = el.binary.value.trim();
  saveSettingsSoon();
});

el.port.addEventListener("input", () => {
  settings.port = el.port.value.trim();
  saveSettingsSoon();
});

el.detect.addEventListener("click", () => {
  if (state && state.detectedBinary) {
    el.binary.value = state.detectedBinary;
    settings.binary = state.detectedBinary;
    saveSettingsSoon();
    toast("تم اكتشاف: " + state.detectedBinary);
  } else {
    toast("لم أجد ملف DOpad. ابنِه بالأمر make build أولًا.", true);
  }
});

el.start.addEventListener("click", startServer);
el.stop.addEventListener("click", stopServer);
el.refreshState.addEventListener("click", refreshState);
el.refreshQr.addEventListener("click", () => refreshQR(true));

async function startServer() {
  const body = {
    mode: settings.mode,
    port: el.port.value.trim(),
    binary: el.binary.value.trim(),
    password: el.password.value,
  };
  el.start.disabled = true;
  el.start.textContent = "جارٍ التشغيل…";
  try {
    state = await api("/api/start", { method: "POST", body });
    clearTimeout(settingsTimer);
    settingsPending = false;
    settingsVersion += 1;
    settings = state.settings || settings;
    el.password.value = "";
    renderState();
    toast("تم بدء التشغيل");
    refreshLogs();
  } catch (error) {
    toast(error.message, true);
    appendLogLine("خطأ: " + error.message, "err");
  } finally {
    el.start.textContent = "تشغيل الخادم";
    refreshState();
  }
}

async function stopServer() {
  el.stop.disabled = true;
  el.stop.textContent = "جارٍ الإيقاف…";
  try {
    state = await api("/api/stop", { method: "POST", body: {} });
    renderState();
    toast("تم إيقاف الخادم");
  } catch (error) {
    toast(error.message, true);
  } finally {
    el.stop.textContent = "إيقاف";
    refreshState();
  }
}

el.copyUrl.addEventListener("click", async () => {
  if (!el.url.value) return;
  try {
    await navigator.clipboard.writeText(el.url.value);
    toast("تم نسخ الرابط");
  } catch {
    toast("تعذّر النسخ", true);
  }
});

el.openUrl.addEventListener("click", () => {
  if (el.url.value) window.open(el.url.value, "_blank", "noopener");
});

el.clearLog.addEventListener("click", async () => {
  try {
    const result = await api("/api/clear-logs", { method: "POST", body: {} });
    lastSeq = result.seq || lastSeq;
    el.log.textContent = "";
    logLines = 0;
  } catch (error) {
    toast(error.message, true);
  }
});

el.quit.addEventListener("click", async () => {
  if (!confirm("إيقاف الخادم وإغلاق اللوحة؟")) return;
  try {
    await api("/api/quit", { method: "POST", body: {} });
    setConn(false, "أُغلقت اللوحة");
    toast("أُغلقت اللوحة. يمكنك إغلاق هذه الصفحة.");
  } catch (error) {
    toast(error.message, true);
  }
});

function lineClass(text) {
  if (/⚠|خطأ|error|تعذّر|لم أجد|فشل|غير صالح/i.test(text)) return "err";
  if (/🟢|يعمل الآن|Open|http:\/\//i.test(text)) return "ok";
  return "";
}

function appendLogLine(text, cls = "") {
  const span = document.createElement("span");
  if (cls) span.className = cls;
  span.textContent = text + "\n";
  el.log.appendChild(span);
  logLines += 1;
  while (logLines > 2200 && el.log.firstChild) {
    el.log.removeChild(el.log.firstChild);
    logLines -= 1;
  }
}

async function refreshLogs() {
  if (document.hidden) return;
  try {
    const result = await api("/api/logs?after=" + lastSeq);
    if (Array.isArray(result.lines) && result.lines.length) {
      let sawUrl = false;
      for (const line of result.lines) {
        appendLogLine(line.text, lineClass(line.text));
        lastSeq = Math.max(lastSeq, line.seq);
        if (/http:\/\/|المنفذ|port/i.test(line.text)) sawUrl = true;
      }
      if (el.autoscroll.checked) el.log.scrollTop = el.log.scrollHeight;
      if (sawUrl) refreshState();
    } else if (typeof result.seq === "number") {
      lastSeq = Math.max(lastSeq, result.seq);
    }
  } catch {
    /* the state poll reports the connection status */
  }
}

/* --------------------------------------------------------------- catalog */

const typeInfo = {
  panel: { icon: "▭", desc: "خلفية بلا لمس" },
  button: { icon: "◉", desc: "زر مستقل" },
  trigger: { icon: "▰", desc: "مزلاق LT/RT" },
  stick: { icon: "✥", desc: "عصا تحكم" },
  pad: { icon: "⊞", desc: "إطار أجزاء" },
};

async function loadCatalog() {
  try {
    catalog = await api("/api/catalog");
    iconCache.clear();
    renderPalette();
    el.catalogNote.textContent =
      "تم تحميل " + catalog.types.length + " أنواع و" + catalog.buttons.length + " زرًا من controls.js";
    if (doc.elements.length) renderAll();
  } catch (error) {
    el.catalogNote.textContent = "تعذّر تحميل controls.js: " + error.message;
    toast("تعذّر تحميل الكتالوج: " + error.message, true);
  }
}

function renderPalette() {
  el.typeList.textContent = "";
  el.buttonGrid.textContent = "";
  el.triggerGrid.textContent = "";
  if (!catalog) return;

  for (const type of catalog.types) {
    const info = typeInfo[type] || { icon: "•", desc: "" };
    const button = document.createElement("button");
    button.type = "button";
    button.className = "type-item";
    button.innerHTML = "";
    const icon = document.createElement("span");
    icon.className = "type-icon";
    icon.textContent = info.icon;
    const text = document.createElement("span");
    const title = document.createElement("b");
    title.textContent = type;
    const desc = document.createElement("small");
    desc.textContent = info.desc;
    text.append(title, desc);
    button.append(icon, text);
    button.addEventListener("click", () => addElement(type));
    el.typeList.appendChild(button);
  }

  for (const item of catalog.buttons) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "key-chip";
    button.textContent = item.key;
    button.title = "إضافة زر " + item.key;
    button.addEventListener("click", () => addButton(item.key));
    el.buttonGrid.appendChild(button);
  }

  for (const key of ["LT", "RT"]) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "key-chip alt";
    button.textContent = key;
    button.title = "إضافة مزلاق " + key;
    button.addEventListener("click", () => addTrigger(key));
    el.triggerGrid.appendChild(button);
  }
}

el.refreshCatalog.addEventListener("click", loadCatalog);

/* --------------------------------------------------------------- editor */

function markDirty() {
  dirty = true;
  el.dirty.classList.remove("hidden");
}

function updateDirty() {
  el.dirty.classList.toggle("hidden", !dirty);
}

function jsonText() {
  return JSON.stringify(doc, null, 2) + "\n";
}

function defaultW(element) {
  if (element.type === "panel") return 20;
  if (element.type === "button") return element.shape === "round" ? 12 : 18;
  if (element.type === "trigger") return 18;
  return 40;
}

function defaultH(element) {
  if (element.type === "panel") return 20;
  if (element.type === "button") return element.shape === "round" ? defaultW(element) : 10;
  if (element.type === "trigger") return 34;
  return 40;
}

function addElement(type) {
  if (!catalog || !catalog.defaults[type]) return;
  const element = deepCopy(catalog.defaults[type]);
  doc.elements.push(element);
  selected = doc.elements.length - 1;
  selectedPart = type === "pad" ? 0 : -1;
  markDirty();
  renderAll();
}

function addButton(key) {
  if (!catalog) return;
  const element = deepCopy(catalog.defaults.button || { type: "button" });
  element.type = "button";
  element.key = key;
  element.label = key;
  element.shape = "round";
  element.anchor = "cc";
  element.x = 0;
  element.y = 0;
  if (key === "LS" || key === "RS") element.latch = 500;
  doc.elements.push(element);
  selected = doc.elements.length - 1;
  selectedPart = -1;
  markDirty();
  renderAll();
}

function addTrigger(key) {
  if (!catalog) return;
  const element = deepCopy(catalog.defaults.trigger || { type: "trigger" });
  element.type = "trigger";
  element.key = key;
  element.label = key;
  element.anchor = "tc";
  element.x = key === "LT" ? -12 : 12;
  element.y = 2;
  doc.elements.push(element);
  selected = doc.elements.length - 1;
  selectedPart = -1;
  markDirty();
  renderAll();
}

function select(index) {
  selected = index >= 0 && index < doc.elements.length ? index : -1;
  selectedPart = -1;
  if (selected >= 0 && doc.elements[selected].type === "pad" && Array.isArray(doc.elements[selected].parts) && doc.elements[selected].parts.length) {
    selectedPart = 0;
  }
  renderStage();
  renderInspector();
  renderLayers();
  updateStageInfo();
}

function deleteSelected() {
  if (selected < 0) return;
  doc.elements.splice(selected, 1);
  selected = -1;
  selectedPart = -1;
  markDirty();
  renderAll();
}

function duplicateSelected() {
  if (selected < 0) return;
  const copy = deepCopy(doc.elements[selected]);
  copy.x = (Number(copy.x) || 0) + 2;
  copy.y = (Number(copy.y) || 0) + 2;
  doc.elements.splice(selected + 1, 0, copy);
  selected += 1;
  markDirty();
  renderAll();
}

function moveLayer(direction) {
  if (selected < 0) return;
  const target = selected + direction;
  if (target < 0 || target >= doc.elements.length) return;
  const [item] = doc.elements.splice(selected, 1);
  doc.elements.splice(target, 0, item);
  selected = target;
  markDirty();
  renderAll();
}

el.del.addEventListener("click", deleteSelected);
el.dup.addEventListener("click", duplicateSelected);
el.raise.addEventListener("click", () => moveLayer(1));
el.lower.addEventListener("click", () => moveLayer(-1));

function setProp(object, key, value) {
  if (value === undefined || value === null || value === "") delete object[key];
  else object[key] = value;
  markDirty();
  renderAll();
}

/* --------------------------------------------------------------- inspector */

const propOrder = [
  "type", "key", "label", "icon", "action", "shape", "color",
  "anchor", "x", "y", "w", "h", "size", "z", "latch",
  "side", "area", "ring", "knob", "travel", "reach", "rest",
  "metric", "spread", "inner", "grow", "fill", "edge",
  "dir", "from", "angle", "arc", "poly", "sweep", "at", "fs", "arrow",
];

function propsFor(object, isPart) {
  let list;
  if (isPart) {
    list = (catalog && catalog.part_props) || [];
  } else {
    list = (catalog && catalog.type_props && catalog.type_props[object.type]) || [];
  }
  const set = new Set(list);
  set.add("type");
  const result = Array.from(set);
  result.sort((a, b) => {
    const ai = propOrder.indexOf(a);
    const bi = propOrder.indexOf(b);
    if (ai < 0 && bi < 0) return a.localeCompare(b);
    if (ai < 0) return 1;
    if (bi < 0) return -1;
    return ai - bi;
  });
  return result;
}

function metaFor(key) {
  return (catalog && catalog.prop_meta && catalog.prop_meta[key]) || { kind: "text", label: key };
}

function optionValue(value) {
  if (value === undefined || value === null) return "";
  return String(value);
}

function makeField(object, key, meta, onChange, readonly = false) {
  const field = document.createElement("div");
  field.className = "field";

  const label = document.createElement("span");
  label.textContent = meta.label || key;
  field.appendChild(label);

  const row = document.createElement("div");
  row.className = "field-row";

  const value = object[key];
  const kind = meta.kind || "text";

  let input;
  if (readonly) {
    input = document.createElement("code");
    input.textContent = optionValue(value) || "—";
    input.style.width = "100%";
  } else if (kind === "bool") {
    const wrap = document.createElement("label");
    wrap.className = "bool-row";
    input = document.createElement("input");
    input.type = "checkbox";
    input.checked = !!value;
    input.addEventListener("change", () => onChange(input.checked));
    const text = document.createElement("span");
    text.textContent = input.checked ? "مفعّل" : "معطّل";
    input.addEventListener("change", () => {
      text.textContent = input.checked ? "مفعّل" : "معطّل";
    });
    wrap.append(input, text);
    row.appendChild(wrap);
    field.appendChild(row);
    if (meta.hint) {
      const hint = document.createElement("div");
      hint.className = "hint";
      hint.textContent = meta.hint;
      field.appendChild(hint);
    }
    return field;
  } else if (kind === "select" || kind === "key" || kind === "icon") {
    input = document.createElement("select");
    let options = meta.options || [];
    if (kind === "key") options = (catalog && catalog.buttons ? catalog.buttons.map((b) => b.key) : []);
    if (kind === "icon") options = catalog && catalog.icons ? Object.keys(catalog.icons) : [];

    if (key !== "type") {
      const blank = document.createElement("option");
      blank.value = "";
      blank.textContent = "— افتراضي —";
      input.appendChild(blank);
    }
    const seen = new Set();
    for (const option of options) {
      if (seen.has(option)) continue;
      seen.add(option);
      const node = document.createElement("option");
      node.value = option;
      node.textContent = option;
      input.appendChild(node);
    }
    const current = optionValue(value);
    if (current && !seen.has(current)) {
      const node = document.createElement("option");
      node.value = current;
      node.textContent = current;
      input.appendChild(node);
    }
    input.value = current;
    input.addEventListener("change", () => onChange(input.value === "" ? undefined : input.value));
  } else if (kind === "number") {
    input = document.createElement("input");
    input.type = "number";
    input.step = "0.5";
    input.value = value === undefined || value === null ? "" : String(value);
    input.addEventListener("change", () => {
      const text = input.value.trim();
      if (text === "") return onChange(undefined);
      const number = Number(text);
      onChange(Number.isFinite(number) ? number : undefined);
    });
  } else if (kind === "dimension") {
    input = document.createElement("input");
    input.type = "text";
    input.spellcheck = false;
    input.value = value === undefined || value === null ? "" : String(value);
    input.placeholder = "رقم u أو 50%";
    input.addEventListener("change", () => {
      const text = input.value.trim();
      if (text === "") return onChange(undefined);
      if (text.endsWith("%")) return onChange(text);
      const number = Number(text);
      onChange(Number.isFinite(number) ? number : text);
    });
  } else if (kind === "pair" || kind === "quad") {
    input = document.createElement("input");
    input.type = "text";
    input.spellcheck = false;
    input.value = Array.isArray(value) ? value.join(", ") : value === undefined || value === null ? "" : String(value);
    input.placeholder = kind === "quad" ? "r1, r2, a0, a1" : "a, b";
    input.addEventListener("change", () => {
      const text = input.value.trim();
      if (text === "") return onChange(undefined);
      const numbers = text.match(/-?\d+(?:\.\d+)?/g);
      onChange(numbers ? numbers.map(Number) : undefined);
    });
  } else if (kind === "poly") {
    input = document.createElement("input");
    input.type = "text";
    input.spellcheck = false;
    input.value = value === undefined || value === null ? "" : String(value);
    input.placeholder = "x,y x,y …";
    input.addEventListener("change", () => {
      const text = input.value.trim().replace(/\s+/g, " ");
      onChange(text === "" ? undefined : text);
    });
  } else {
    input = document.createElement("input");
    input.type = "text";
    input.spellcheck = false;
    input.value = value === undefined || value === null ? "" : String(value);
    input.addEventListener("change", () => {
      const text = input.value.trim();
      onChange(text === "" ? undefined : text);
    });
  }

  row.appendChild(input);

  if (!readonly && key !== "type" && value !== undefined) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "remove";
    remove.title = "حذف الخاصية";
    remove.textContent = "×";
    remove.addEventListener("click", () => onChange(undefined));
    row.appendChild(remove);
  }

  field.appendChild(row);
  if (meta.hint) {
    const hint = document.createElement("div");
    hint.className = "hint";
    hint.textContent = meta.hint;
    field.appendChild(hint);
  }
  return field;
}

function renderInspector() {
  const element = doc.elements[selected];
  el.inspectorFields.textContent = "";
  el.partsSection.classList.add("hidden");

  if (!element) {
    el.selType.textContent = "لم يتم اختيار عنصر";
    el.selBadge.textContent = "—";
    el.inspectorEmpty.classList.remove("hidden");
    return;
  }

  el.inspectorEmpty.classList.add("hidden");
  const label = element.key ? element.type + " • " + element.key : element.type;
  el.selType.textContent = label;
  el.selBadge.textContent = element.type;

  const props = propsFor(element, false);
  for (const key of props) {
    if (key === "parts") continue;
    const meta = metaFor(key);
    const readonly = key === "type";
    el.inspectorFields.appendChild(
      makeField(element, key, meta, (value) => setProp(element, key, value), readonly)
    );
  }

  if (element.type === "pad") {
    el.partsSection.classList.remove("hidden");
    renderParts();
  }
}

function renderParts() {
  const element = doc.elements[selected];
  if (!element || element.type !== "pad") return;
  if (!Array.isArray(element.parts)) element.parts = [];
  if (selectedPart < 0 && element.parts.length) selectedPart = 0;
  if (selectedPart >= element.parts.length) selectedPart = element.parts.length - 1;

  el.partsList.textContent = "";
  element.parts.forEach((part, index) => {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "part-item" + (index === selectedPart ? " active" : "");
    const title = document.createElement("span");
    title.textContent = "#" + (index + 1) + " " + (part.type || "button");
    if (part.key) title.textContent += " • " + (Array.isArray(part.key) ? part.key.join("+") : part.key);
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = part.arc ? "قوس" : part.poly ? "مضلّع" : "بلا شكل";
    item.append(title, tag);
    item.addEventListener("click", () => {
      selectedPart = index;
      renderParts();
    });
    el.partsList.appendChild(item);
  });

  renderPartFields();
}

function renderPartFields() {
  const element = doc.elements[selected];
  el.partFields.textContent = "";
  if (!element || element.type !== "pad") return;
  const part = (element.parts || [])[selectedPart];
  if (!part) return;

  const props = propsFor(part, true);
  for (const key of props) {
    const meta = metaFor(key);
    el.partFields.appendChild(
      makeField(part, key, meta, (value) => {
        if (value === undefined || value === null || value === "") delete part[key];
        else part[key] = value;
        markDirty();
        renderAll();
      })
    );
  }
}

el.addPartButton.addEventListener("click", () => {
  const element = doc.elements[selected];
  if (!element || element.type !== "pad" || !catalog) return;
  if (!Array.isArray(element.parts)) element.parts = [];
  element.parts.push(deepCopy(catalog.part_defaults.button));
  selectedPart = element.parts.length - 1;
  markDirty();
  renderAll();
});

el.addPartTrigger.addEventListener("click", () => {
  const element = doc.elements[selected];
  if (!element || element.type !== "pad" || !catalog) return;
  if (!Array.isArray(element.parts)) element.parts = [];
  element.parts.push(deepCopy(catalog.part_defaults.trigger));
  selectedPart = element.parts.length - 1;
  markDirty();
  renderAll();
});

el.deletePart.addEventListener("click", () => {
  const element = doc.elements[selected];
  if (!element || element.type !== "pad" || !Array.isArray(element.parts)) return;
  if (selectedPart < 0) return;
  element.parts.splice(selectedPart, 1);
  selectedPart = Math.min(selectedPart, element.parts.length - 1);
  markDirty();
  renderAll();
});

function renderLayers() {
  el.layers.textContent = "";
  if (!doc.elements.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "لا توجد عناصر بعد. أضف عنصرًا من المكتبة.";
    el.layers.appendChild(empty);
    return;
  }

  for (let index = doc.elements.length - 1; index >= 0; index -= 1) {
    const element = doc.elements[index];
    const item = document.createElement("button");
    item.type = "button";
    item.className = "layer" + (index === selected ? " active" : "");

    const swatch = document.createElement("span");
    swatch.className = "swatch";
    if (element.color) swatch.style.background = "var(--" + element.color + ")";
    else if (element.type === "panel") swatch.style.background = "#2a3550";

    const title = document.createElement("span");
    title.textContent = element.type + (element.key ? " • " + element.key : element.label ? " • " + element.label : "");

    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = "#" + (index + 1);

    item.append(swatch, title, tag);
    item.addEventListener("click", () => select(index));
    el.layers.appendChild(item);
  }
}

/* --------------------------------------------------------------- stage */

function posPx(value, axis) {
  if (typeof value === "number") return value * stageU;
  if (typeof value === "string") {
    const text = value.trim();
    if (text.endsWith("%")) {
      const percent = parseFloat(text) / 100;
      return (axis === "x" ? stageW : stageH) * (Number.isFinite(percent) ? percent : 0);
    }
    const number = parseFloat(text);
    return Number.isFinite(number) ? number * stageU : 0;
  }
  return 0;
}

function dimPx(value, axis) {
  return posPx(value, axis);
}

function rectFor(element, width, height) {
  const anchor = element.anchor || "tl";
  const x = posPx(element.x || 0, "x");
  const y = posPx(element.y || 0, "y");
  if (anchor === "cc") {
    return { x: stageW / 2 + x - width / 2, y: stageH / 2 + y - height / 2, w: width, h: height };
  }
  let left;
  let top;
  if (anchor[0] === "t") top = y;
  else top = stageH - y - height;
  if (anchor[1] === "l") left = x;
  else if (anchor[1] === "r") left = stageW - x - width;
  else left = stageW / 2 + x - width / 2;
  return { x: left, y: top, w: width, h: height };
}

function defaultZ(element) {
  if (element.z !== undefined && element.z !== null) return Number(element.z) || 0;
  if (element.type === "panel") return 0;
  if (element.type === "stick") return element.area ? 1 : 4;
  return 3;
}

function trigDir(element) {
  const dir = String(element.dir ?? element.from ?? "top").toLowerCase();
  if (dir === "bottom" || dir === "down") return "bottom";
  if (dir === "left") return "left";
  if (dir === "right") return "right";
  return "top";
}

function elementNode(element, index) {
  const node = document.createElement("div");
  node.className = "el";
  node.dataset.index = String(index);
  node.addEventListener("pointerdown", onElementDown);

  let rect;
  if (element.type === "panel") {
    const w = dimPx(element.w ?? 20, "x");
    const h = dimPx(element.h ?? 20, "y");
    rect = rectFor(element, w, h);
    node.classList.add("panel");
    if (element.fill) node.classList.add("fill");
    if (element.edge === "right" || element.edge === "left") node.classList.add("edge-" + element.edge);
    if (element.label) {
      const label = document.createElement("div");
      label.className = "panel-label";
      label.textContent = element.label;
      node.appendChild(label);
    }
  } else if (element.type === "button") {
    const round = element.shape === "round";
    const w = dimPx(element.w ?? (round ? 12 : 18), "x");
    const h = round ? w : dimPx(element.h ?? 10, "y");
    rect = rectFor(element, w, h);
    node.classList.add("btn");
    node.classList.add(round ? "round" : "pill");
    if (element.color) node.classList.add("c-" + element.color);
    node.style.fontSize = round && !element.icon ? w * 0.42 + "px" : 4 * stageU + "px";
    if (element.icon && catalog && catalog.icons[element.icon]) {
      node.innerHTML = catalog.icons[element.icon];
    } else {
      node.textContent = element.label ?? element.key ?? "";
    }
  } else if (element.type === "trigger") {
    const w = dimPx(element.w ?? 18, "x");
    const h = dimPx(element.h ?? 34, "y");
    rect = rectFor(element, w, h);
    const dir = trigDir(element);
    node.classList.add("trig", "dir-" + dir);
    const fill = document.createElement("div");
    fill.className = "fill";
    node.appendChild(fill);
    const label = document.createElement("div");
    label.className = "lbl";
    label.textContent = element.label ?? element.key ?? "";
    if (Array.isArray(element.at)) {
      label.style.inset = "auto";
      label.style.left = element.at[0] + "%";
      label.style.top = element.at[1] + "%";
      label.style.transform = "translate(-50%, -50%)";
      label.style.padding = "0";
      label.style.display = "block";
    }
    node.appendChild(label);
  } else if (element.type === "stick") {
    if (element.area) {
      const diameter = (element.ring ?? 36) * stageU;
      const cx = element.area === "right" ? stageW * 0.75 : stageW * 0.25;
      const cy = stageH / 2;
      rect = { x: cx - diameter / 2, y: cy - diameter / 2, w: diameter, h: diameter };
      node.classList.add("stick-area");
      node.textContent = element.area === "right" ? "R" : "L";
      node.title = "منطقة لمس العصا";
    } else {
      const size = dimPx(element.size ?? element.w ?? 40, "x");
      const height = dimPx(element.h ?? element.size ?? element.w ?? 40, "y");
      rect = rectFor(element, size, height);
      node.classList.add("stick-box");
      const ringSize = (element.ring ?? 36) * stageU;
      const knobSize = (element.knob ?? (element.ring ?? 36) * 0.43) * stageU;
      const ring = document.createElement("div");
      ring.className = "fring";
      ring.style.width = ring.style.height = ringSize + "px";
      ring.style.left = rect.w / 2 + "px";
      ring.style.top = rect.h / 2 + "px";
      const knob = document.createElement("div");
      knob.className = "fknob";
      knob.style.width = knob.style.height = knobSize + "px";
      knob.style.left = rect.w / 2 + "px";
      knob.style.top = rect.h / 2 + "px";
      node.append(ring, knob);
    }
  } else if (element.type === "pad") {
    const w = dimPx(element.w ?? element.size ?? 40, "x");
    const h = dimPx(element.h ?? element.w ?? element.size ?? 40, "y");
    rect = rectFor(element, w, h);
    node.classList.add("pad");
    const svg = svgEl("svg", { viewBox: "0 0 100 100", class: "pad-svg" });
    renderPad(svg, element);
    node.appendChild(svg);
  } else {
    rect = { x: 0, y: 0, w: 0, h: 0 };
  }

  node.style.left = rect.x + "px";
  node.style.top = rect.y + "px";
  node.style.width = rect.w + "px";
  node.style.height = rect.h + "px";
  return node;
}

function renderStage() {
  stageW = el.stage.clientWidth || stageW;
  stageH = el.stage.clientHeight || stageH;
  if (!stageW || !stageH) return;
  stageU = Math.min(stageH / 100, stageW * 0.0055);
  el.stage.style.setProperty("--u", stageU + "px");

  el.stageElements.textContent = "";
  const ordered = (doc.elements || [])
    .map((element, index) => ({ element, index, z: defaultZ(element) }))
    .sort((a, b) => a.z - b.z || a.index - b.index);

  for (const item of ordered) {
    el.stageElements.appendChild(elementNode(item.element, item.index));
  }
  updateSelectionOverlay();
  updateStageInfo();
}

function updateSelectionOverlay() {
  const node = el.stageElements.querySelector('.el[data-index="' + selected + '"]');
  const element = doc.elements[selected];
  if (!node || !element) {
    el.selection.classList.add("hidden");
    el.handle.classList.add("hidden");
    return;
  }

  const left = node.offsetLeft;
  const top = node.offsetTop;
  const width = node.offsetWidth;
  const height = node.offsetHeight;

  el.selection.classList.remove("hidden");
  el.selection.style.left = left - 2 + "px";
  el.selection.style.top = top - 2 + "px";
  el.selection.style.width = width + 4 + "px";
  el.selection.style.height = height + 4 + "px";

  const areaStick = element.type === "stick" && !!element.area;
  el.handle.classList.toggle("hidden", areaStick);
  if (!areaStick) {
    el.handle.style.left = left + width - 8 + "px";
    el.handle.style.top = top + height - 8 + "px";
  }
}

function updateStageInfo() {
  const count = (doc.elements || []).length;
  const element = doc.elements[selected];
  let text = count + " عنصر";
  if (element) text += " • محدد: " + element.type + (element.key ? " (" + element.key + ")" : "");
  el.stageInfo.textContent = text;
}

function resizeStage() {
  const wrap = el.stageWrap.getBoundingClientRect();
  const maxWidth = Math.max(220, wrap.width - 8);
  const maxHeight = Math.max(150, wrap.height - 34);
  let width = maxWidth;
  let height = (width * 9) / 16;
  if (height > maxHeight) {
    height = maxHeight;
    width = (height * 16) / 9;
  }
  el.stage.style.width = Math.floor(width) + "px";
  el.stage.style.height = Math.floor(height) + "px";
  stageW = el.stage.clientWidth;
  stageH = el.stage.clientHeight;
  stageU = Math.min(stageH / 100, stageW * 0.0055);
  el.stage.style.setProperty("--u", stageU + "px");
  renderStage();
}

/* --------------------------------------------------------------- pad SVG */

const SVG_NS = "http://www.w3.org/2000/svg";

function svgEl(tag, attrs = {}, className = "") {
  const node = document.createElementNS(SVG_NS, tag);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value);
  if (className) node.setAttribute("class", className);
  return node;
}

function pt(radius, angle) {
  return [
    50 + radius * Math.sin((angle * Math.PI) / 180),
    50 - radius * Math.cos((angle * Math.PI) / 180),
  ];
}

function arcPath(r1, r2, a0, a1) {
  let span = ((a1 - a0) % 360 + 360) % 360;
  if (span === 0) span = 360;
  const large = span > 180 ? 1 : 0;
  const [x0, y0] = pt(r2, a0);
  const [x1, y1] = pt(r2, a1);
  const [x2, y2] = pt(r1, a1);
  const [x3, y3] = pt(r1, a0);
  return `M${x0} ${y0}A${r2} ${r2} 0 ${large} 1 ${x1} ${y1}L${x2} ${y2}A${r1} ${r1} 0 ${large} 0 ${x3} ${y3}Z`;
}

function polyPts(value) {
  return String(value || "")
    .trim()
    .split(/\s+/)
    .map((pair) => pair.split(",").map(Number))
    .filter((pair) => pair.length === 2 && Number.isFinite(pair[0]) && Number.isFinite(pair[1]));
}

function shapeAt(part) {
  if (Array.isArray(part.arc) && part.arc.length >= 4) {
    const [r1, r2, a0, a1] = part.arc;
    return pt((r1 + r2) / 2, (a0 + a1) / 2);
  }
  if (part.poly) {
    const points = polyPts(part.poly);
    if (!points.length) return [50, 50];
    let area = 0;
    let cx = 0;
    let cy = 0;
    for (let i = 0; i < points.length; i += 1) {
      const [x0, y0] = points[i];
      const [x1, y1] = points[(i + 1) % points.length];
      const cross = x0 * y1 - x1 * y0;
      area += cross;
      cx += (x0 + x1) * cross;
      cy += (y0 + y1) * cross;
    }
    if (Math.abs(area) > 1e-6) return [cx / (3 * area), cy / (3 * area)];
    const xs = points.map((point) => point[0]);
    const ys = points.map((point) => point[1]);
    return [(Math.min(...xs) + Math.max(...xs)) / 2, (Math.min(...ys) + Math.max(...ys)) / 2];
  }
  return [50, 50];
}

const iconCache = new Map();

function iconInner(name) {
  if (!catalog || !catalog.icons || !catalog.icons[name]) return "";
  if (iconCache.has(name)) return iconCache.get(name);
  let inner = "";
  try {
    const parsed = new DOMParser().parseFromString(catalog.icons[name], "image/svg+xml");
    const svg = parsed.querySelector("svg");
    if (svg) inner = svg.innerHTML;
  } catch {
    inner = "";
  }
  iconCache.set(name, inner);
  return inner;
}

function renderPad(svg, element) {
  const parts = Array.isArray(element.parts) ? element.parts : [];
  for (const part of parts) {
    const kind = part.type === "trigger" ? "trigger" : "button";
    const hasShape = !!(part.arc || part.poly);
    if (!hasShape) continue;

    let shape;
    if (part.arc) {
      shape = svgEl("path", { d: arcPath(...part.arc) });
    } else {
      shape = svgEl("polygon", { points: part.poly });
    }
    let className = "part";
    if (part.poly) className += " edged";
    if (part.color) className += " c-" + part.color;
    if (kind === "trigger") className += " trig-base";
    shape.setAttribute("class", className);
    svg.appendChild(shape);

    if (part.arrow) {
      const [tx, ty] = shapeAt(part);
      const rotation = part.angle ?? (part.arc ? (part.arc[2] + part.arc[3]) / 2 : 0);
      svg.appendChild(
        svgEl("polygon", {
          points: "-5,3.5 5,3.5 0,-4.5",
          transform: `translate(${tx} ${ty}) rotate(${rotation})`,
          class: "arw",
        })
      );
    }

    if (part.icon || part.label) {
      const [lx, ly] = Array.isArray(part.at) ? part.at : shapeAt(part);
      if (part.icon && catalog && catalog.icons[part.icon]) {
        const size = part.size ?? 14;
        const icon = svgEl("svg", {
          x: lx - size / 2,
          y: ly - size / 2,
          width: size,
          height: size,
          viewBox: "0 0 24 24",
          class: "pico",
        });
        icon.innerHTML = iconInner(part.icon);
        svg.appendChild(icon);
      } else if (part.label) {
        const text = svgEl("text", { x: lx, y: ly }, "plbl" + (part.color ? "" : " lt"));
        if (part.fs) text.setAttribute("font-size", part.fs);
        text.textContent = part.label;
        svg.appendChild(text);
      }
    }
  }
}

/* --------------------------------------------------------------- pointer */

function onElementDown(event) {
  if (event.button !== 0) return;
  const index = Number(event.currentTarget.dataset.index);
  select(index);
  const element = doc.elements[index];
  if (!element) return;
  if (element.type === "stick" && element.area) return;
  drag = {
    index,
    startX: event.clientX,
    startY: event.clientY,
    x: Number(element.x) || 0,
    y: Number(element.y) || 0,
    anchor: element.anchor || "tl",
    moved: false,
  };
  try {
    el.stage.setPointerCapture(event.pointerId);
  } catch {
    /* ignore */
  }
  event.preventDefault();
  event.stopPropagation();
}

el.stage.addEventListener("pointerdown", (event) => {
  if (event.target.closest(".el") || event.target === el.handle) return;
  select(-1);
});

el.handle.addEventListener("pointerdown", (event) => {
  if (selected < 0) return;
  const element = doc.elements[selected];
  if (!element) return;
  if (element.type === "stick" && element.area) return;
  resize = {
    index: selected,
    startX: event.clientX,
    startY: event.clientY,
    w: element.w,
    h: element.h,
    size: element.size,
    type: element.type,
    shape: element.shape,
    moved: false,
  };
  try {
    el.stage.setPointerCapture(event.pointerId);
  } catch {
    /* ignore */
  }
  event.preventDefault();
  event.stopPropagation();
});

function onPointerMove(event) {
  if (drag) {
    const element = doc.elements[drag.index];
    if (!element) return;
    const du = (event.clientX - drag.startX) / stageU;
    const dv = (event.clientY - drag.startY) / stageU;
    const anchor = drag.anchor;
    const x = drag.x + (anchor.endsWith("r") ? -du : du);
    const y = drag.y + (anchor.startsWith("b") ? -dv : dv);
    element.x = round1(x);
    element.y = round1(y);
    drag.moved = true;
    markDirty();
    renderStage();
    event.preventDefault();
    return;
  }

  if (resize) {
    const element = doc.elements[resize.index];
    if (!element) return;
    const du = (event.clientX - resize.startX) / stageU;
    const dv = (event.clientY - resize.startY) / stageU;
    const delta = Math.max(du, dv);

    const usesSize =
      resize.size !== undefined ||
      resize.type === "pad" ||
      (resize.type === "stick" && !element.area);

    if (usesSize) {
      const base = Number(resize.size ?? element.size ?? element.w ?? 40);
      element.size = round1(Math.max(4, base + delta));
      delete element.w;
      delete element.h;
    } else {
      const baseW = resize.w ?? element.w ?? defaultW(element);
      const baseH = resize.h ?? element.h ?? defaultH(element);
      element.w = resizeDim(baseW, du, "x");
      if (!(resize.type === "button" && resize.shape === "round")) {
        element.h = resizeDim(baseH, dv, "y");
      }
    }
    resize.moved = true;
    markDirty();
    renderStage();
    event.preventDefault();
  }
}

function resizeDim(base, delta, axis) {
  if (typeof base === "string" && base.trim().endsWith("%")) {
    const percent = parseFloat(base) || 0;
    const extra = (delta / (axis === "x" ? stageW : stageH)) * 100;
    return round1(Math.max(1, percent + extra)) + "%";
  }
  const number = Number(base);
  return round1(Math.max(1, (Number.isFinite(number) ? number : 0) + delta));
}

function onPointerUp(event) {
  const changed = (drag && drag.moved) || (resize && resize.moved);
  drag = null;
  resize = null;
  try {
    if (el.stage.hasPointerCapture(event.pointerId)) el.stage.releasePointerCapture(event.pointerId);
  } catch {
    /* ignore */
  }
  if (changed) {
    markDirty();
    renderInspector();
    renderStage();
  }
}

el.stage.addEventListener("pointermove", onPointerMove);
el.stage.addEventListener("pointerup", onPointerUp);
el.stage.addEventListener("pointercancel", onPointerUp);

document.addEventListener("keydown", (event) => {
  if (activeTab !== "editor") return;
  const tag = (event.target && event.target.tagName) || "";
  if (tag === "INPUT" || tag === "SELECT" || tag === "TEXTAREA") return;

  if (event.key === "Delete" || event.key === "Backspace") {
    event.preventDefault();
    deleteSelected();
    return;
  }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "d") {
    event.preventDefault();
    duplicateSelected();
    return;
  }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
    event.preventDefault();
    saveLayout();
    return;
  }
  if (event.key === "Escape") {
    select(-1);
    return;
  }
  if (selected >= 0 && ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) {
    const element = doc.elements[selected];
    if (!element) return;
    event.preventDefault();
    const step = event.shiftKey ? 2 : 0.5;
    const x = Number(element.x) || 0;
    const y = Number(element.y) || 0;
    if (event.key === "ArrowLeft") element.x = round1(x - step);
    if (event.key === "ArrowRight") element.x = round1(x + step);
    if (event.key === "ArrowUp") element.y = round1(y - step);
    if (event.key === "ArrowDown") element.y = round1(y + step);
    markDirty();
    renderStage();
    renderInspector();
  }
});

/* --------------------------------------------------------------- layouts */

async function loadLayouts() {
  try {
    layouts = await api("/api/layouts");
    updateLayoutSelect();
  } catch (error) {
    toast("تعذّر تحميل قائمة التخطيطات: " + error.message, true);
  }
}

function updateLayoutSelect() {
  const current = layoutId;
  el.layoutSelect.textContent = "";
  if (!layouts.length) {
    const option = document.createElement("option");
    option.value = "";
    option.textContent = "— لا توجد تخطيطات —";
    el.layoutSelect.appendChild(option);
    return;
  }
  for (const item of layouts) {
    const option = document.createElement("option");
    option.value = item.id;
    option.textContent = item.name + " (" + item.id + ")";
    el.layoutSelect.appendChild(option);
  }
  if (layouts.some((item) => item.id === current)) el.layoutSelect.value = current;
}

function validateDoc(data) {
  if (!data || typeof data !== "object" || Array.isArray(data)) throw new Error("الملف ليس كائن JSON");
  if (!Array.isArray(data.elements)) throw new Error("الحقل elements مفقود أو ليس مصفوفة");
  for (const element of data.elements) {
    if (!element || typeof element !== "object" || typeof element.type !== "string") {
      throw new Error("يوجد عنصر بلا نوع صالح");
    }
  }
}

function applyDoc(data, id, notify = false) {
  validateDoc(data);
  doc = data;
  if (typeof doc.name !== "string") doc.name = id || "تخطيطي";
  layoutId = id || "my-layout";
  el.layoutId.value = layoutId;
  el.layoutName.value = doc.name;
  selected = -1;
  selectedPart = -1;
  dirty = false;
  updateDirty();
  renderAll();
  if (layouts.some((item) => item.id === layoutId)) el.layoutSelect.value = layoutId;
  if (notify) toast("تم تحميل " + (doc.name || layoutId));
}

async function loadLayout(id) {
  if (dirty && !confirm("لديك تغييرات غير محفوظة. تحميل التخطيط وضياعها؟")) return;
  try {
    const data = await api("/api/layouts/" + encodeURIComponent(id));
    applyDoc(data, id, true);
  } catch (error) {
    toast(error.message, true);
  }
}

function newLayout() {
  if (dirty && !confirm("لديك تغييرات غير محفوظة. بدء تخطيط جديد وضياعها؟")) return;
  doc = { name: "تخطيطي", elements: [] };
  layoutId = "my-layout";
  el.layoutId.value = layoutId;
  el.layoutName.value = doc.name;
  selected = -1;
  selectedPart = -1;
  dirty = false;
  updateDirty();
  renderAll();
  toast("تخطيط جديد");
}

async function saveLayout() {
  const id = el.layoutId.value.trim();
  if (!/^[A-Za-z0-9._-]+$/.test(id)) {
    toast("اسم الملف يجب أن يستخدم حروفًا إنجليزية وأرقامًا و . _ - فقط", true);
    return;
  }
  doc.name = el.layoutName.value.trim() || id;
  layoutId = id;
  try {
    const result = await api("/api/layouts/" + encodeURIComponent(id), {
      method: "POST",
      body: jsonText(),
    });
    dirty = false;
    updateDirty();
    await loadLayouts();
    toast("تم الحفظ: " + result.path);
  } catch (error) {
    toast(error.message, true);
  }
}

function exportJson() {
  const id = (el.layoutId.value.trim() || "layout").replace(/[^A-Za-z0-9._-]+/g, "-");
  doc.name = el.layoutName.value.trim() || id;
  const blob = new Blob([jsonText()], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = id + ".json";
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1500);
  toast("تم تصدير " + id + ".json");
}

el.loadLayout.addEventListener("click", () => {
  const id = el.layoutSelect.value;
  if (id) loadLayout(id);
});

el.newLayout.addEventListener("click", newLayout);
el.saveLayout.addEventListener("click", saveLayout);
el.exportBtn.addEventListener("click", exportJson);

el.copyJson.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(jsonText());
    toast("تم نسخ JSON إلى الحافظة");
  } catch (error) {
    toast("تعذّر النسخ: " + error.message, true);
  }
});

el.importBtn.addEventListener("click", () => {
  if (dirty && !confirm("لديك تغييرات غير محفوظة. استيراد ملف وضياعها؟")) return;
  el.importFile.click();
});
el.importFile.addEventListener("change", () => {
  const file = el.importFile.files && el.importFile.files[0];
  if (!file) return;
  const reader = new FileReader();
  reader.onload = () => {
    try {
      const data = JSON.parse(String(reader.result || ""));
      validateDoc(data);
      applyDoc(data, file.name.replace(/\.json$/i, ""), false);
      dirty = true;
      updateDirty();
      toast("تم استيراد " + file.name);
    } catch (error) {
      toast("تعذّر استيراد الملف: " + error.message, true);
    }
  };
  reader.onerror = () => toast("تعذّرت قراءة الملف", true);
  reader.readAsText(file);
  el.importFile.value = "";
});

el.layoutSelect.addEventListener("change", () => {
  /* loading is explicit via the load button */
});

/* --------------------------------------------------------------- render */

function renderAll() {
  renderStage();
  renderInspector();
  renderLayers();
  updateStageInfo();
  updateDirty();
}

async function initEditor() {
  await loadLayouts();
  if (layouts.some((item) => item.id === "classic")) {
    try {
      const data = await api("/api/layouts/classic");
      applyDoc(data, "classic", false);
    } catch {
      newLayout();
    }
  } else {
    newLayout();
  }
  editorReady = true;
}

/* --------------------------------------------------------------- boot */

async function boot() {
  await loadCatalog();
  await initEditor();
  await refreshState();
  await refreshLogs();

  setInterval(refreshState, 1500);
  setInterval(refreshLogs, 900);
  setInterval(updateUptime, 1000);
  setInterval(() => {
    if (state && state.running && !qrObjectURL) refreshQR(true);
  }, 4000);

  if (typeof ResizeObserver !== "undefined") {
    new ResizeObserver(() => {
      if (activeTab === "editor") resizeStage();
    }).observe(el.stageWrap);
  }
  window.addEventListener("resize", () => {
    if (activeTab === "editor") resizeStage();
  });

  requestAnimationFrame(() => {
    resizeStage();
    renderStage();
  });
}

window.addEventListener("beforeunload", (event) => {
  if (!dirty) return;
  event.preventDefault();
  event.returnValue = "";
});

boot().catch((error) => {
  console.error(error);
  toast("تعذّر بدء الواجهة: " + error.message, true);
});
