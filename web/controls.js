"use strict";
const $ = (s) => document.querySelector(s);
const stage = $("#stage"), statusEl = $("#status"), ui = $("#ui");
const playersEl = $("#players"), layoutsEl = $("#layouts"), errEl = $("#err"), goEl = $("#go");
const store = {
  get(k, d) { try { return localStorage.getItem(k) ?? d; } catch (_) { return d; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch (_) {} },
};

/* ---------- بيئة التشغيل ----------
   iPhone لا يدعم Fullscreen API للصفحات (يدعمها iPad فقط): لا يمكن لأي
   JavaScript إخفاء شريط عنوان Safari أو أدواته. الحل الحقيقي هو تشغيل
   الصفحة كتطبيق مثبَّت على الشاشة الرئيسية (standalone). */
const isIPhone = /iPhone|iPod/.test(navigator.userAgent);
const isStandalone = () => navigator.standalone === true ||
  matchMedia("(display-mode: standalone)").matches ||
  matchMedia("(display-mode: fullscreen)").matches;

/* =====================================================================
   الحالة المرسَلة إلى الحاسوب (8 بايت)
   ===================================================================== */
const BTN = { UP: 1, DOWN: 2, LEFT: 4, RIGHT: 8, START: 16, BACK: 32, LS: 64, RS: 128,
              LB: 256, RB: 512, HOME: 1024, A: 4096, B: 8192, X: 16384, Y: 32768 };
const BIT_COUNT = 16; // عدد بتات الأزرار في الحزمة (uint16)
const NEUTRAL = { lx: 0, ly: 0, rx: 0, ry: 0, btn: 0, lt: 0, rt: 0 };
const state = { ...NEUTRAL };
const DEAD = 0.08;
const clamp01 = (v) => Math.max(0, Math.min(1, v)); // يحصر القيمة بين 0 و1
// أزمنة الإرسال وإعادة الاتصال (مللي ثانية)
const SEND_MS = 50, POLL_MS = 2000, STABLE_MS = 3000, RETRY_MS = 1000, RETRY_CAP_MS = 4000, MAX_RETRIES = 6;
const U = (n) => (typeof n === "string" ? n : `calc(var(--u) * ${n})`); // رقم = وحدة u، نص = كما هو
let dirty = true, playing = false, paused = false, ws = null, slot = 1, sawTouch = false;

const bits = new Array(BIT_COUNT).fill(0); // عدّاد لكل زر: لمستان على الزر نفسه لا تتعارضان
function press(mask) {
  for (let i = 0; i < BIT_COUNT; i++) if ((mask >> i) & 1) { if (bits[i]++ === 0) state.btn |= 1 << i; }
  dirty = true;
}
function release(mask) {
  for (let i = 0; i < BIT_COUNT; i++) if (((mask >> i) & 1) && bits[i] > 0) { if (--bits[i] === 0) state.btn &= ~(1 << i); }
  dirty = true;
}
function setStick(side, x, y) { // x و y بين -1 و 1 (الأعلى موجب)
  const m = Math.hypot(x, y);
  if (m < DEAD) { x = y = 0; } else if (m > 1) { x /= m; y /= m; }
  const k = side === "left" ? "l" : "r";
  state[k + "x"] = Math.round(x * 127); state[k + "y"] = Math.round(y * 127);
  dirty = true;
}
function setTrigger(key, v) { state[key] = Math.round(clamp01(v) * 255); dirty = true; }
// أزرار «التثبيت» (latch): الضغط المطوّل يثبّت الزر، والنقرة التالية تحرّره.
const latches = [];
function resetLatches() { for (const l of latches) l.reset(); }
function releaseAll() {
  resetLatches();
  for (const id of [...owner.keys()]) stop(id);
  Object.assign(state, NEUTRAL);
  bits.fill(0); dirty = true; send();
}

/* =====================================================================
   موجّه اللمس: كل لمسة تُسند عند بدايتها إلى منطقة واحدة وتبقى لها حتى ترفع إصبعك.
   المناطق المسجّلة لاحقًا لها أولوية أعلى. المنطقة: { hit, down, move, up }
   ===================================================================== */
const zones = [], owner = new Map();
let curZ = 3; // طبقة العنصر الجاري بناؤه: تحدد ما فوق ما (عرضًا وأولوية لمس)
const zone = (z) => { z.z = curZ; z.i = zones.length; zones.push(z); return z; };
function start(id, x, y) {
  if (paused) return;
  for (let i = zones.length - 1; i >= 0; i--) {
    const z = zones[i];
    if (z.hit(x, y) && z.down(id, x, y) !== false) { owner.set(id, z); return; }
  }
}
function move(id, x, y) { const z = owner.get(id); if (z && z.move) z.move(id, x, y); }
function stop(id) { const z = owner.get(id); if (z) { owner.delete(id); z.up(id); } }
function sync(e) { // لو ضاع touchend لأي سبب، أنهِ اللمسات التي لم تعد موجودة فعلًا
  const alive = new Set();
  for (const t of e.touches) alive.add(t.identifier);
  for (const id of [...owner.keys()]) if (typeof id === "number" && !alive.has(id)) stop(id);
}

const dbg = new URLSearchParams(location.search).has("debug") ? Object.assign(document.createElement("pre"), { id: "dbg" }) : null;
if (dbg) document.body.appendChild(dbg);
const dbgLines = [];
function log(e) {
  if (!dbg) return;
  dbgLines.push(`${e.type}  touches=${e.touches.length}  owners=${owner.size}`);
  if (dbgLines.length > 8) dbgLines.shift();
  dbg.textContent = dbgLines.join("\n");
}

const live = (e) => playing && !(e.target.closest && e.target.closest("#ui"));
const opt = { passive: false };
document.addEventListener("touchstart", (e) => {
  if (!live(e)) return;
  e.preventDefault(); sawTouch = true;
  for (const t of e.changedTouches) start(t.identifier, t.clientX, t.clientY);
  sync(e); log(e);
}, opt);
document.addEventListener("touchmove", (e) => {
  if (!live(e)) return;
  e.preventDefault();
  for (const t of e.changedTouches) move(t.identifier, t.clientX, t.clientY);
}, opt);
const touchEnd = (e) => {
  if (!live(e)) return;
  e.preventDefault();
  for (const t of e.changedTouches) stop(t.identifier);
  sync(e); log(e);
};
document.addEventListener("touchend", touchEnd, opt);
document.addEventListener("touchcancel", touchEnd, opt);
// الفأرة: للتجربة على الحاسوب فقط
let mouseDown = false;
document.addEventListener("mousedown", (e) => { if (!live(e) || sawTouch) return; mouseDown = true; start("mouse", e.clientX, e.clientY); });
document.addEventListener("mousemove", (e) => { if (mouseDown) move("mouse", e.clientX, e.clientY); });
document.addEventListener("mouseup", () => { if (mouseDown) { mouseDown = false; stop("mouse"); } });

/* =====================================================================
   مكونات اللمس
   ===================================================================== */
const inside = (el, x, y) => { const r = el.getBoundingClientRect(); return x >= r.left && x <= r.right && y >= r.top && y <= r.bottom; };
// قيمة 0..1 داخل مستطيل {x,y,w,h} حسب اتجاه التعبئة (top/bottom/left/right).
const linearValue = (dir, x, y, r) => {
  const v = dir === "bottom" ? (r.y + r.h - y) / r.h : dir === "left" ? (x - r.x) / r.w
          : dir === "right" ? (r.x + r.w - x) / r.w : (y - r.y) / r.h;
  return clamp01(v);
};
// latch (ms): الضغط المتواصل لهذه المدة يثبّت الزر؛ النقرة التالية تحرّره.
function button(el, mask, holdMs) {
  const hit = (x, y) => inside(el, x, y);
  if (holdMs == null || !(holdMs >= 0)) { // زر عادي بلا تثبيت
    zone({ hit,
           down: () => { press(mask); el.classList.add("on"); },
           up: () => { release(mask); el.classList.remove("on"); } });
    return;
  }
  let active = false, latched = false, timer = 0, ignoreUp = false;
  const rec = {
    reset() {
      clearTimeout(timer); timer = 0;
      if (active) release(mask); // لا نترك بتًّا معلّقًا عند الإيقاف/القائمة
      active = latched = ignoreUp = false;
      el.classList.remove("on", "latched");
    },
  };
  latches.push(rec);
  const down = () => {
    if (ignoreUp) return;
    if (latched) { // نقرة على زر مثبَّت = تحرير فقط
      release(mask); active = latched = false;
      el.classList.remove("on", "latched");
      ignoreUp = true;
      return;
    }
    if (active) return;
    active = true; press(mask); el.classList.add("on");
    if (holdMs <= 0) { latched = true; el.classList.add("latched"); return; }
    timer = setTimeout(() => {
      timer = 0;
      if (!active) return;
      latched = true; el.classList.add("latched");
    }, holdMs);
  };
  const up = () => {
    if (ignoreUp) { ignoreUp = false; return; }
    if (latched) return; // يبقى مضغوطًا حتى النقرة التالية
    if (!active) return;
    clearTimeout(timer); timer = 0;
    active = false; release(mask); el.classList.remove("on");
  };
  zone({ hit, down, up });
}
function tap(el, fn) {
  zone({ hit: (x, y) => inside(el, x, y), down: () => { el.classList.add("on"); fn(); }, up: () => el.classList.remove("on") });
}
function slider(el, key, dir = "top") { // مزلاق مستطيل مستقل: التعبئة من الاتجاه المحدد
  const setFill = (v) => el.style.setProperty("--fill", v * 100 + "%");
  const set = (x, y) => {
    const r = el.getBoundingClientRect();
    const v = linearValue(dir, x, y, { x: r.left, y: r.top, w: r.width || 1, h: r.height || 1 });
    setTrigger(key, v); setFill(v);
  };
  zone({ hit: (x, y) => inside(el, x, y),
         down: (id, x, y) => set(x, y), move: (id, x, y) => set(x, y),
         up: () => { setTrigger(key, 0); setFill(0); } });
}
// منطقة تحوّل موضع اللمسة إلى قناع أزرار (للأسهم وأزرار ABXY). كل إصبع مستقل عن الآخر.
// mask(x,y) -> قناع الأزرار المضغوطة،  visual(unionMask) -> لتلوين الأجزاء المضغوطة
// latchParts: أجزاء تحمل خاصية latch [{ mask, ms }] فتبقى مضغوطة بعد رفع الإصبع.
function maskZone({ hit, mask, visual, latchParts = [], grab }) {
  const held = new Map();     // إصبع -> مقبض مزلاق التقطه عند بداية اللمس (يبقى له حتى الرفع)
  const cur = new Map();      // إصبع -> قناع أجزائه الحالية
  const suppressed = new Map(); // إصبع -> أجزاء حُرِّرت بنقرته (كي لا تُثبَّت مجددًا)
  const timers = new Map();   // جزء -> مؤقّت التثبيت
  const latchedParts = new Set(); // فهارس الأجزاء المثبَّتة
  let applied = 0;            // اتحاد ما ضغطته هذه المنطقة فعلًا (لمنع التعارض)
  const union = () => { let u = 0; for (const m of cur.values()) u |= m; return u; };
  const latchedMask = () => { let u = 0; for (const i of latchedParts) u |= latchParts[i].mask; return u; };
  const recompute = () => {
    let u = latchedMask() | union();
    for (const [id, sm] of suppressed) if (cur.has(id)) u &= ~sm;
    const add = u & ~applied, del = applied & ~u;
    if (add) press(add);
    if (del) release(del);
    applied = u; visual(u);
  };
  const syncTimers = () => {
    const held = union();
    latchParts.forEach((p, i) => {
      const isHeld = p.mask !== 0 && (held & p.mask) === p.mask;
      if (isHeld && !latchedParts.has(i)) {
        if (!timers.has(i)) timers.set(i, setTimeout(() => {
          timers.delete(i); latchedParts.add(i); recompute();
        }, p.ms));
      } else if (!isHeld && timers.has(i)) {
        clearTimeout(timers.get(i)); timers.delete(i);
      }
    });
  };
  if (latchParts.length) latches.push({ reset() {
    for (const t of timers.values()) clearTimeout(t);
    timers.clear(); latchedParts.clear(); suppressed.clear(); recompute();
  }});
  zone({ hit,
    down(id, x, y) {
      const g = grab && grab(x, y);
      if (g) { held.set(id, g); g.move(id, x, y); return; }
      const nm = mask(x, y);
      let sm = 0;
      latchParts.forEach((p, i) => { // لمس جزء مثبَّت = تحريره فقط
        if ((nm & p.mask) && latchedParts.has(i)) {
          latchedParts.delete(i); sm |= p.mask;
          if (timers.has(i)) { clearTimeout(timers.get(i)); timers.delete(i); }
        }
      });
      cur.set(id, nm & ~sm);
      if (sm) suppressed.set(id, sm);
      recompute(); syncTimers();
    },
    move(id, x, y) {
      const g = held.get(id);
      if (g) { g.move(id, x, y); return; }
      if (!cur.has(id)) return;
      cur.set(id, mask(x, y) & ~(suppressed.get(id) || 0));
      recompute(); syncTimers();
    },
    up(id) {
      const g = held.get(id);
      if (g) { held.delete(id); g.up(id); return; }
      cur.delete(id); suppressed.delete(id); recompute(); syncTimers();
    },
  });
}

/* =====================================================================
   بناء التخطيط من ملف JSON
   ===================================================================== */
const NS = "http://www.w3.org/2000/svg";
const svgEl = (tag, attrs, cls) => {
  const n = document.createElementNS(NS, tag);
  for (const k in attrs) n.setAttribute(k, attrs[k]);
  if (cls) n.setAttribute("class", cls);
  return n;
};
const div = (cls) => Object.assign(document.createElement("div"), { className: cls });
const mkButton = (label, cls, fn) => Object.assign(document.createElement("button"), { className: cls, textContent: label, onclick: fn });
const pt = (r, a) => [50 + r * Math.sin(a * Math.PI / 180), 50 - r * Math.cos(a * Math.PI / 180)];
const angOf = (dx, dy) => (Math.atan2(dx, -dy) * 180 / Math.PI + 360) % 360; // 0 = أعلى، مع عقارب الساعة
const angDiff = (a, b) => { const d = Math.abs(a - b) % 360; return d > 180 ? 360 - d : d; };
// فترة التثبيت بالمللي ثانية (null = الزر لا يملك الخاصية)
const numMs = (v) => { if (v == null || typeof v === "boolean") return null; const n = Number(v); return Number.isFinite(n) ? Math.max(0, n) : null; };
// أشكال مخصصة بنفس اصطلاح pad: arc [r1,r2,a0,a1] أو poly "x,y x,y …" بإحداثيات 0..100.
const polyPts = (s) => String(s).trim().split(/\s+/).map((p) => p.split(",").map(Number));
function arcPath(r1, r2, a0, a1) {
  const span = ((a1 - a0) % 360 + 360) % 360;
  const large = span > 180 ? 1 : 0;
  const [x0, y0] = pt(r2, a0), [x1, y1] = pt(r2, a1), [x2, y2] = pt(r1, a1), [x3, y3] = pt(r1, a0);
  return `M${x0} ${y0}A${r2} ${r2} 0 ${large} 1 ${x1} ${y1}L${x2} ${y2}A${r1} ${r1} 0 ${large} 0 ${x3} ${y3}Z`;
}
// ---- هندسة أجزاء إطار pad: كل الإحداثيات 0..100 حول المركز (50,50) ----
function shapePts(p) { // نقاط حدود الشكل (للصندوق المحيط ونطاق القطاع)
  if (p.poly) return polyPts(p.poly);
  const [r1, r2, a0, a1] = p.arc;
  let span = ((a1 - a0) % 360 + 360) % 360;
  if (span === 0) span = 360;
  const n = Math.max(2, Math.ceil(span / 10)), out = [];
  for (let i = 0; i <= n; i++) { const a = a0 + span * i / n; out.push(pt(r1, a), pt(r2, a)); }
  return out;
}
function autoSweep(pts) { // أصغر قطاع زاوي حول المركز يضم كل رؤوس المضلّع
  const angs = pts.filter(([x, y]) => Math.hypot(x - 50, y - 50) > 1).map(([x, y]) => angOf(x - 50, y - 50)).sort((a, b) => a - b);
  if (angs.length < 2) return [0, 360];
  let gap = -1, at = 0;
  for (let i = 0; i < angs.length; i++) {
    const nxt = i + 1 < angs.length ? angs[i + 1] : angs[0] + 360;
    if (nxt - angs[i] > gap) { gap = nxt - angs[i]; at = (i + 1) % angs.length; }
  }
  return [angs[at], angs[at] + 360 - gap];
}
function wedge(a0, a1) { // قطاع من المركز بنصف قطر 90 يغطي أي شكل داخل الإطار
  const n = Math.max(1, Math.ceil(Math.abs(a1 - a0) / 20)), out = ["50,50"];
  for (let i = 0; i <= n; i++) { const [x, y] = pt(90, a0 + (a1 - a0) * i / n); out.push(x.toFixed(2) + "," + y.toFixed(2)); }
  return out.join(" ");
}
// مركز الشكل: منتصف شريط القوس أو مركز ثقل المضلّع (موضع النص/السهم الافتراضي)
function shapeAt(o) {
  if (o.arc) {
    const [r1, r2, a0, a1] = o.arc, mid = (a0 + a1) / 2, r = (r1 + r2) / 2;
    return pt(r, mid);
  }
  if (o.poly) {
    const pts = polyPts(o.poly);
    let a = 0, cx = 0, cy = 0;
    for (let i = 0; i < pts.length; i++) {
      const [x0, y0] = pts[i], [x1, y1] = pts[(i + 1) % pts.length];
      const cross = x0 * y1 - x1 * y0;
      a += cross; cx += (x0 + x1) * cross; cy += (y0 + y1) * cross;
    }
    if (Math.abs(a) > 1e-6) return [cx / (3 * a), cy / (3 * a)];
    const xs = pts.map((p) => p[0]), ys = pts.map((p) => p[1]);
    return [(Math.min(...xs) + Math.max(...xs)) / 2, (Math.min(...ys) + Math.max(...ys)) / 2];
  }
  return [50, 50];
}
// اتجاه تعبئة المزلاق: من الأعلى/الأسفل/اليسار/اليمين (up/down مقبولان كبديلين)
function trigDir(o) {
  const d = String(o.dir ?? o.from ?? "top").toLowerCase();
  return d === "bottom" || d === "down" ? "bottom" : d === "left" ? "left" : d === "right" ? "right" : "top";
}
const trigKey = (key) => (key === "RT" ? "rt" : "lt");

const ICONS = {
  home: '<svg viewBox="0 0 24 24"><path d="M12 3 3 11h2.5v9h5v-6h3v6h5v-9H21z" fill="currentColor"/></svg>',
  view: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="8" y="3" width="11" height="14" rx="1.5"/><rect x="5" y="7" width="11" height="14" rx="1.5" fill="#34405c"/></svg>',
  menu: '<svg viewBox="0 0 24 24"><path d="M7 6v12M12 6v12M17 6v12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>',
  gear: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><circle cx="12" cy="12" r="8" stroke-width="3.2" stroke-dasharray="2.09 2.1"/><circle cx="12" cy="12" r="6" stroke-width="2"/><circle cx="12" cy="12" r="2.2" stroke-width="2"/></svg>',
};

// يضع العنصر على الشاشة. الأرقام بوحدة u (≈ 1% من ارتفاع الشاشة) والنصوص مثل "50%" نسبة من الشاشة.
// anchor = tl | tr | bl | br | tc | bc | cc  (الزوايا، منتصف الحافة العلوية/السفلية، أو منتصف الشاشة تمامًا)
// و x و y إزاحة من نقطة الارتكاز. cc رأسيًّا وأفقيًّا: left/top = 50% + الإزاحة − نصف الحجم.
function place(el, o, w, h) {
  const a = o.anchor || "tl", x = o.x || 0, y = o.y || 0;
  el.classList.add("el");
  el.style.zIndex = curZ;
  el.style.width = U(w); el.style.height = U(h);
  if (a === "cc") {
    el.style.top = `calc(50% + ${U(y)} - ${U(h)} / 2)`;
    el.style.left = `calc(50% + ${U(x)} - ${U(w)} / 2)`;
  } else {
    if (a[0] === "t") el.style.top = U(y); else el.style.bottom = U(y);
    if (a[1] === "l") el.style.left = `calc(${U(x)} + var(--sl))`;
    else if (a[1] === "r") el.style.right = `calc(${U(x)} + var(--sr))`;
    else el.style.left = `calc(50% + ${U(x)} - ${U(w)} / 2)`;
  }
  stage.appendChild(el);
}
const uPx = () => $("#probe").getBoundingClientRect().width; // قيمة u بالبكسل

/* =====================================================================
   كتالوج العناصر — هنا يُعرَّف «ما هي الأزرار وما خصائصها» فقط.
   التخطيط كله (ماذا يوجد، أين، بأي حجم، وبأي أجزاء) يُكتب في ملف JSON داخل layouts/ .
   خصائص مشتركة لكل العناصر: anchor, x, y (الموضع)، w, h (الحجم) أو size (مربع)،
   z (الطبقة: الأعلى يظهر فوق ويأخذ اللمس أولًا. الافتراضي: panel=0، stick بـ area=1، stick بصندوق=4، البقية=3).

   panel    لوحة خلفية بلا لمس .......... fill, edge ("left"|"right"), label
   button   زر مستقل بسيط ................ key (A B X Y LB RB BACK START HOME LS RS) أو action:"menu"،
                                           label أو icon (home view menu gear)، shape (pill|round)، color (a b x y)،
                                           latch (مللي ثانية: الضغط المطوّل يثبّت الزر، والنقرة التالية تحرّره)
   trigger  مزلاق مستطيل مستقل ............ key (LT|RT)، label، dir (top|bottom|left|right) لاتجاه التعبئة
   stick    عصا (الصفر = نقطة لمس الإصبع)  side (left|right)، منطقة اللمس: area ("left"|"right" نصف الشاشة)
                                           أو الصندوق (anchor,x,y,size) مع reach (نسبة من نصف قطره)،
                                           ring (قطر الحلقة)، knob (قطر المقبض)، travel (أقصى إزاحة)، rest (إظهارها وهي ساكنة)
   pad      إطار (Frame) بمركز مشترك وإحداثيات 0..100: يجمع أجزاء من أي نوع
            الإطار: size أو w/h، metric ("circle"|"diamond")، spread (تسامح الزاوية)، inner (منطقة ميتة)،
                    reach، grow (تسامح البعد حول arc، افتراضي 6)
            كل جزء: type ("button" الافتراضي | "trigger")، angle (اتجاهه من الأعلى مع عقارب الساعة)،
                    arc [r1,r2,a0,a1] أو poly "x,y x,y …"، spread، color، label/icon مع at [x,y]، fs
              button : key (زر أو مصفوفة أزرار)، arrow، latch
              trigger: key (LT|RT)، dir = cw|ccw (قطاعي، الافتراضي للقوس) | out|in (شعاعي) | top|bottom|left|right (خطي، الافتراضي للمضلّع)،
                       sweep [a0,a1] لمدى الزاوية في المضلّعات (يُحسب تلقائيًا إن حُذف)
   ===================================================================== */
let clipSeq = 0; // معرّفات clipPath فريدة لتعبئة المزالق داخل الإطارات
const hooks = []; // دوال تُعاد عند تغيّر حجم الشاشة (لمواضع العصي وهي ساكنة)
const BUILD = {
  panel(o) {
    const el = div("panel" + (o.fill ? " fill" : "") + (o.edge ? " edge-" + o.edge[0] : ""));
    if (o.label) { const t = div("hint"); t.textContent = o.label; el.appendChild(t); }
    place(el, o, o.w ?? 20, o.h ?? 20);
  },

  button(o) {
    const round = o.shape === "round";
    const w = o.w ?? (round ? 12 : 18), h = round ? w : (o.h ?? 10);
    const el = div("b " + (round ? "round" : "pill") + (o.color ? " c-" + o.color : ""));
    if (o.icon) el.innerHTML = ICONS[o.icon] || ""; else el.textContent = o.label ?? o.key ?? "";
    if (round && !o.icon) el.style.fontSize = `calc(var(--u) * ${w * 0.42})`;
    place(el, o, w, h);
    if (o.action === "menu") tap(el, openMenu);
    else if (BTN[o.key]) button(el, BTN[o.key], numMs(o.latch));
    else console.warn("زر بلا key صالح:", o);
  },

  trigger(o) {
    const dir = trigDir(o);
    const el = div("b trig dir-" + dir);
    el.style.setProperty("--fill", "0%");
    el.innerHTML = `<i></i><span></span>`;
    const span = el.querySelector("span");
    span.textContent = o.label ?? o.key;
    if (o.at) { span.style.inset = "auto"; span.style.left = o.at[0] + "%"; span.style.top = o.at[1] + "%";
                span.style.transform = "translate(-50%, -50%)"; span.style.padding = "0"; }
    place(el, o, o.w ?? 18, o.h ?? 34);
    slider(el, trigKey(o.key), dir);
  },

  stick(o) {
    const side = o.side === "right" ? "right" : "left";
    const ringU = o.ring ?? 36, knobU = o.knob ?? +(ringU * 0.43).toFixed(2), travelU = o.travel ?? ringU / 2;
    let box = null; // الصندوق المرجعي (موضع العصا الساكنة ومنطقة اللمس) عند عدم استعمال area
    if (!o.area) { const s = o.size ?? o.w ?? 40; box = div("el sbox"); place(box, o, s, o.h ?? s); }
    const ring = div("fring"), knob = div("fknob");
    ring.style.width = ring.style.height = U(ringU);
    knob.style.width = knob.style.height = U(knobU);
    ring.style.zIndex = knob.style.zIndex = curZ;
    stage.append(ring, knob);
    const showRest = o.rest ?? !o.area;
    const center = () => { const r = box.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; };
    const put = (n, x, y) => { // إحداثيات المتصفح → إحداثيات #stage
      const r = stage.getBoundingClientRect();
      n.style.left = (x - r.left) + "px"; n.style.top = (y - r.top) + "px";
    };
    const rest = () => {
      if (showRest && box) { const [cx, cy] = center(); for (const n of [ring, knob]) { put(n, cx, cy); n.style.display = "block"; } }
      else ring.style.display = knob.style.display = "none";
    };
    hooks.push(rest);

    let id = null, ox = 0, oy = 0;
    const follow = (x, y) => {
      const lim = travelU * uPx();
      let dx = x - ox, dy = y - oy; const d = Math.hypot(dx, dy);
      if (d > lim) { dx = dx / d * lim; dy = dy / d * lim; }
      put(knob, ox + dx, oy + dy);
      setStick(side, dx / lim, -dy / lim); // الإزاحة عن نقطة اللمس لا عن المنتصف
    };
    zone({
      hit: (x, y) => {
        if (o.area) return (o.area === "left") === (x < innerWidth / 2);
        const [cx, cy] = center(), R = box.getBoundingClientRect().width / 2;
        return Math.hypot(x - cx, y - cy) <= (o.reach ?? 1) * R;
      },
      down(tid, x, y) {
        if (id !== null) return false; // العصا مشغولة بإصبع آخر
        id = tid; ox = x; oy = y;
        ring.classList.remove("free"); knob.classList.remove("free");
        for (const n of [ring, knob]) { put(n, x, y); n.style.display = "block"; } // الحلقة تنتقل تحت الإصبع
        follow(x, y);
      },
      move(tid, x, y) { if (tid === id) follow(x, y); },
      up(tid) {
        if (tid !== id) return;
        id = null; setStick(side, 0, 0);
        ring.classList.add("free"); knob.classList.add("free");
        rest(); // تعود لمكانها (أو تختفي إن كانت عائمة)
      },
    });
  },

  // pad = إطار (Frame): مساحة مربعة بمركز مشترك (50,50) وإحداثيات 0..100. كل جزء يعلن نوعه: "button" (الافتراضي) أو "trigger".
  // لغة موحّدة لكل الأجزاء: angle = اتجاه الجزء حول المركز، arc = [r1,r2,a0,a1] = بعد الحافة الداخلية، الخارجية، زاوية حافتي البداية والنهاية.
  // trigger: مزلاق تناظري يُمسَك من أول لمسة؛ التعبئة قطاعية (cw/ccw) أو شعاعية (out/in) أو خطية (top/bottom/left/right).
  pad(o) {
    const w = o.w ?? o.size ?? 40, h = o.h ?? w;
    const el = div("cl"); place(el, o, w, h);
    const svg = svgEl("svg", { viewBox: "0 0 100 100" }); el.appendChild(svg);
    const defs = svgEl("defs", {}); svg.appendChild(defs);
    const diamond = o.metric === "diamond";
    const spread = o.spread ?? 60, inner = o.inner ?? 0.2, reach = o.reach ?? 1.08, grow = o.grow ?? 6;
    const metric = (dx, dy) => (diamond ? Math.abs(dx) + Math.abs(dy) : Math.hypot(dx, dy));
    // الإطار مربع بضلع min(w,h) في منتصف العنصر، ونحوّل اللمسة إلى إحداثيات 0..100
    const toLocal = (x, y) => {
      const r = el.getBoundingClientRect(), s = Math.min(r.width, r.height) || 1;
      return [50 + (x - (r.left + r.width / 2)) / s * 100, 50 + (y - (r.top + r.height / 2)) / s * 100];
    };

    const parts = [];
    for (const p of o.parts || []) {
      const kind = p.type === "trigger" ? "trigger" : "button";
      const hasShape = !!(p.arc || p.poly);
      if (kind === "trigger" && !hasShape) { console.warn("مزلاق داخل pad يحتاج arc أو poly:", p); continue; }
      const mask = kind === "button" ? [].concat(p.key || []).reduce((m, k) => m | (BTN[k] || 0), 0) : 0;
      const cls = "part" + (p.poly ? " edged" : "") + (p.color ? " cc c-" + p.color : "");
      let shape = null;
      if (p.arc) shape = svgEl("path", { d: arcPath(...p.arc) }, cls);
      else if (p.poly) shape = svgEl("polygon", { points: p.poly }, cls);
      if (shape) svg.appendChild(shape);
      // الاتجاه والمدى: angle (الاتجاه من المركز) و spread (التسامح الزاوي)؛ وإن حُذف angle اشتُق من الشكل
      const spts = hasShape ? shapePts(p) : null;
      // المدى الزاوي: sweep صريح، أو زاويتا القوس، أو أصغر قطاع يضم رؤوس المضلّع.
      const sw = hasShape ? (p.sweep || (p.arc ? [p.arc[2], p.arc[3]] : autoSweep(spts))) : null;
      let span = sw ? ((sw[1] - sw[0]) % 360 + 360) % 360 : 0;
      if (sw && span === 0) span = 360;
      const angle = p.angle ?? (hasShape ? sw[0] + span / 2 : 0);
      const sp = p.spread ?? (p.angle == null && hasShape ? span / 2 + 5 : spread);
      // البعد عن المركز: من arc أو رؤوس المضلّع؛ وإن حدّد الإطار inner/reach صراحةً فهما الحدّان كما في pad الأصلي
      const rlo = p.arc ? p.arc[0] : spts ? Math.min(...spts.map((q) => metric(q[0] - 50, q[1] - 50))) : inner * 50;
      const rhi = p.arc ? p.arc[1] : spts ? Math.max(...spts.map((q) => metric(q[0] - 50, q[1] - 50))) : reach * 50;
      const rec = { kind, mask, angle, sp, rlo, rhi,
        lo: o.inner != null ? inner * 50 : rlo - grow, hi: o.reach != null ? reach * 50 : rhi + grow,
        shape, latch: numMs(p.latch), trig: null };
      if (kind === "trigger") {
        const key = trigKey(p.key);
        const dir = String(p.dir ?? ((p.arc || p.sweep) ? "cw" : "top")).toLowerCase();
        const mode = (dir === "cw" || dir === "ccw") ? "sweep" : (dir === "out" || dir === "in") ? "radial" : "linear";
        const lin = trigDir({ dir });
        const xs = spts.map((q) => q[0]), ys = spts.map((q) => q[1]);
        const bb = { x: Math.min(...xs), y: Math.min(...ys), w: (Math.max(...xs) - Math.min(...xs)) || 1, h: (Math.max(...ys) - Math.min(...ys)) || 1 };
        const rs = [rlo, rhi];
        const clipId = "tc" + (++clipSeq);
        const cp = svgEl("clipPath", { id: clipId });
        const region = svgEl(mode === "radial" ? "path" : mode === "linear" ? "rect" : "polygon", {});
        if (mode === "radial" && dir === "in") region.setAttribute("clip-rule", "evenodd");
        cp.appendChild(region); defs.appendChild(cp);
        const fill = p.arc ? svgEl("path", { d: arcPath(...p.arc) }) : svgEl("polygon", { points: p.poly });
        fill.setAttribute("class", "tfill"); fill.setAttribute("clip-path", `url(#${clipId})`);
        svg.appendChild(fill);
        const disk = (r) => diamond ? `M${50 - r} 50L50 ${50 - r}L${50 + r} 50L50 ${50 + r}Z`
                                    : `M${50 - r} 50a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0Z`;
        const setFill = (v) => {
          if (mode === "sweep") {
            const a = dir === "ccw" ? sw[0] + span - v * span : sw[0], b = dir === "ccw" ? sw[0] + span : sw[0] + v * span;
            region.setAttribute("points", v > 0 ? wedge(a, b) : "");
          } else if (mode === "radial") {
            const rr = dir === "in" ? rs[1] - v * (rs[1] - rs[0]) : rs[0] + v * (rs[1] - rs[0]);
            region.setAttribute("d", dir === "in" ? "M-50 -50H150V150H-50Z" + disk(rr) : disk(rr));
          } else {
            const f = lin === "top" ? [bb.x, bb.y, bb.w, bb.h * v] : lin === "bottom" ? [bb.x, bb.y + bb.h * (1 - v), bb.w, bb.h * v]
                    : lin === "left" ? [bb.x, bb.y, bb.w * v, bb.h] : [bb.x + bb.w * (1 - v), bb.y, bb.w * v, bb.h];
            region.setAttribute("x", f[0]); region.setAttribute("y", f[1]); region.setAttribute("width", f[2]); region.setAttribute("height", f[3]);
          }
        };
        setFill(0);
        const valueAt = (vx, vy) => {
          let v;
          if (mode === "sweep") {
            const t = ((angOf(vx - 50, vy - 50) - sw[0]) % 360 + 360) % 360;
            v = t <= span ? t / span : ((t - span) < (360 - t) ? 1 : 0); // خارج القطاع: أقرب نهاية
            if (dir === "ccw") v = 1 - v;
          } else if (mode === "radial") {
            v = (metric(vx - 50, vy - 50) - rs[0]) / ((rs[1] - rs[0]) || 1);
            if (dir === "in") v = 1 - v;
          } else {
            v = linearValue(lin, vx, vy, bb);
          }
          return clamp01(v);
        };
        const vals = new Map(); // إصبع -> قيمته (تُؤخذ القيمة الأكبر عند تعدد الأصابع)
        const apply = () => { let v = 0; for (const x of vals.values()) v = Math.max(v, x); setTrigger(key, v); setFill(v); };
        rec.trig = {
          move(id, x, y) { const [vx, vy] = toLocal(x, y); vals.set(id, valueAt(vx, vy)); apply(); },
          up(id) { vals.delete(id); apply(); },
        };
      }

      if (hasShape && p.arrow) { // سهم باتجاه الجزء، فوق التعبئة
        const [tx, ty] = shapeAt(p), rot = p.angle ?? (p.arc ? (p.arc[2] + p.arc[3]) / 2 : 0);
        svg.appendChild(svgEl("polygon", { points: "-5,3.5 5,3.5 0,-4.5", transform: `translate(${tx} ${ty}) rotate(${rot})` }, "arw"));
      }
      if (hasShape && (p.label || p.icon)) { // نص أو أيقونة: عند at أو مركز الشكل
        const [lx, ly] = p.at || shapeAt(p);
        if (p.icon && ICONS[p.icon]) {
          const tmp = document.createElement("div"); tmp.innerHTML = ICONS[p.icon];
          const ic = tmp.firstElementChild, sz = p.size ?? 14;
          ic.setAttribute("x", lx - sz / 2); ic.setAttribute("y", ly - sz / 2);
          ic.setAttribute("width", sz); ic.setAttribute("height", sz); ic.setAttribute("class", "pico");
          svg.appendChild(ic);
        } else {
          const t = svgEl("text", { x: lx, y: ly }, "plbl" + (p.color ? "" : " lt"));
          if (p.fs) t.setAttribute("font-size", p.fs);
          t.textContent = p.label; svg.appendChild(t);
        }
      }
      parts.push(rec);
    }

    // كل جزء: اتجاه (angle ± spread) ونطاق بعد (arc). عند تداخل أجزاء بنفس الاتجاه يفوز صاحب النطاق الأقرب،
    // وعند التساوي (كأسهم D-pad) تُعتمد كلها فيعمل القطر. start=true: بداية اللمس (يُحدّه reach)، وإلا استمرار (لا حدّ خارجي).
    const rdOf = (q, r) => Math.max(q.rlo - r, r - q.rhi, 0);
    const pick = (vx, vy, start) => {
      const dx = vx - 50, dy = vy - 50, r = metric(dx, dy), a = angOf(dx, dy);
      const cand = parts.filter((q) => angDiff(a, q.angle) <= q.sp && r >= q.lo && (!start || r <= q.hi));
      if (!cand.length) return cand;
      const best = Math.min(...cand.map((q) => rdOf(q, r)));
      return cand.filter((q) => rdOf(q, r) <= best + 0.01);
    };
    maskZone({
      hit: (x, y) => {
        const [vx, vy] = toLocal(x, y);
        return pick(vx, vy, true).length > 0 || (o.inner != null && metric(vx - 50, vy - 50) < 50 * inner); // المنطقة الميتة تُلتقط أيضًا
      },
      grab: (x, y) => { const c = pick(...toLocal(x, y), true).find((q) => q.trig); return c ? c.trig : null; },
      mask: (x, y) => {
        let m = 0;
        for (const q of pick(...toLocal(x, y), false)) if (q.kind === "button") m |= q.mask;
        return m;
      },
      visual: (m) => parts.forEach((q) => q.shape && q.kind === "button" && q.shape.classList.toggle("on", q.mask !== 0 && !!(m & q.mask))),
      latchParts: parts.filter((q) => q.kind === "button" && q.latch != null).map((q) => ({ mask: q.mask, ms: q.latch })),
    });
  },
};

const relayout = () => hooks.forEach((f) => f());
function clearStage() { stage.innerHTML = ""; zones.length = 0; hooks.length = 0; }

/* ارتفاع/عرض «الشاشة المرئية» فعلًا. على iPhone يبقى شريط أدوات Safari
   ظاهرًا، و100vh أكبر من المساحة المتاحة؛ لذلك نقيس visualViewport ونضبط
   به حجم #stage ووحدة القياس --u. يتغيّر القياس عند تدوير الجهاز أو
   انكماش شريط الأدوات، فتُعاد مواضع العصي الساكنة تلقائيًا. */
function fitViewport() {
  const vv = window.visualViewport;
  const h = Math.round(vv ? vv.height : innerHeight);
  const w = Math.round(vv ? vv.width : innerWidth);
  const r = document.documentElement.style;
  r.setProperty("--vvh", h + "px");
  r.setProperty("--vvw", w + "px");
  r.setProperty("--vvt", Math.round(vv ? vv.offsetTop : 0) + "px");
  r.setProperty("--vvl", Math.round(vv ? vv.offsetLeft : 0) + "px");
  r.setProperty("--sh", (h / 100) + "px");
  if (playing) relayout();
}
addEventListener("resize", fitViewport);
addEventListener("orientationchange", () => setTimeout(fitViewport, 150));
if (window.visualViewport) {
  visualViewport.addEventListener("resize", fitViewport);
  visualViewport.addEventListener("scroll", fitViewport);
}
fitViewport();

function build(spec) {
  clearStage();
  let hasMenu = false;
  for (const o of spec.elements || []) {
    if (!BUILD[o.type]) { console.warn("نوع عنصر غير معروف:", o.type); continue; }
    if (o.action === "menu") hasMenu = true;
    if ((o.type === "button" || o.type === "trigger") && (o.arc || o.poly))
      console.warn("arc/poly لم تعد مدعومة في button/trigger المستقلين: ضعها كجزء داخل إطار pad", o);
    // الطبقة الافتراضية: panel=0، stick بمنطقة نصف شاشة=1، stick بصندوق=4 (فوق الأسهم)، البقية=3
    curZ = o.z ?? (o.type === "panel" ? 0 : o.type === "stick" ? (o.area ? 1 : 4) : 3);
    BUILD[o.type](o);
  }
  curZ = 3;
  // لو نسي التخطيط زر القائمة نضيف واحدًا كي لا يعلق المستخدم
  if (!hasMenu) BUILD.button({ type: "button", action: "menu", icon: "gear", shape: "round", w: 9, anchor: "bc", y: 2 });
  zones.sort((a, b) => a.z - b.z || a.i - b.i); // الأعلى طبقةً يأخذ اللمس أولًا؛ وعند التساوي الأحدث في الملف
}

/* =====================================================================
   الإرسال + الاتصال (WebSocket: يعمل على HTTP على كل المتصفحات بما فيها
   Safari/Brave على iOS — بلا STUN ولا شهادة ولا إعدادات)
   ===================================================================== */
const buf = new ArrayBuffer(8), view = new DataView(buf);
function send() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  view.setInt8(0, state.lx); view.setInt8(1, state.ly);
  view.setInt8(2, state.rx); view.setInt8(3, state.ry);
  view.setUint16(4, state.btn & 0xffff, true);
  view.setUint8(6, state.lt); view.setUint8(7, state.rt);
  ws.send(buf);
}
(function loop() { if (dirty) { send(); dirty = false; } requestAnimationFrame(loop); })();
setInterval(send, SEND_MS); // نبضة تعوّض أي رسالة ضائعة

const setStatus = (t) => { statusEl.textContent = t; };
function closeConn() { const w = ws; ws = null; if (w) { try { w.close(); } catch (_) {} } }

function overlay(msg, buttons) {
  ui.innerHTML = "";
  const p = document.createElement("p"); p.textContent = msg; ui.appendChild(p);
  const row = div("row"); ui.appendChild(row);
  for (const [label, fn, alt] of buttons)
    row.appendChild(mkButton(label, "big" + (alt ? " alt" : ""), fn));
  ui.hidden = false; paused = true; releaseAll();
}
function closeOverlay() { ui.hidden = true; paused = false; }

/* ---------- ملء الشاشة ---------- */
const fsElement = () => document.fullscreenElement || document.webkitFullscreenElement || null;
const fsRequest = () => document.documentElement.requestFullscreen ||
  document.documentElement.webkitRequestFullscreen || document.documentElement.webkitRequestFullScreen;
const fsExit = () => document.exitFullscreen || document.webkitExitFullscreen || document.webkitCancelFullScreen;
// iPhone (بخلاف iPad) لا يدعم Fullscreen API للصفحات؛ لا داعي لمحاولة فاشلة.
const canFullscreen = () => !isIPhone && !!fsRequest();

async function enterFullscreen() {
  const req = fsRequest();
  if (!req) return false;
  try {
    const p = req.call(document.documentElement);
    if (p && p.then) await p;
    try { if (screen.orientation && screen.orientation.lock) await screen.orientation.lock("landscape"); } catch (_) {}
    return true;
  } catch (_) { return false; }
}
async function exitFullscreen() {
  try { if (screen.orientation && screen.orientation.unlock) screen.orientation.unlock(); } catch (_) {}
  const ex = fsExit();
  if (ex) { try { await ex.call(document); } catch (_) {} }
}
function fullscreenHelp() {
  overlay(
    "الشاشة الكاملة على iPhone تحتاج إضافة DOpad إلى الشاشة الرئيسية:\n\n" +
    "١. اضغط زر المشاركة ⤴ في المتصفح\n" +
    "٢. اختر «إضافة إلى الشاشة الرئيسية»\n" +
    "٣. افتح DOpad من الأيقونة الجديدة\n\n" +
    "حينها تُفتح اللعبة بلا شريط عنوان أو أدوات.",
    [["متابعة", closeOverlay], ["القائمة الرئيسية", backToMenu, true]]
  );
}
function openMenu() {
  const btns = [["متابعة", closeOverlay]];
  if (fsElement() || canFullscreen())
    btns.push([fsElement() ? "إنهاء ملء الشاشة" : "ملء الشاشة", toggleFullscreen, true]);
  else if (isIPhone && !isStandalone())
    btns.push(["شاشة كاملة", toggleFullscreen, true]); // يعرض خطوات التثبيت
  btns.push(["القائمة الرئيسية", backToMenu, true]);
  overlay("القائمة", btns);
}
async function toggleFullscreen(e) {
  const b = e && e.currentTarget; // التقطه قبل أي await: currentTarget يصبح null بعدها
  if (fsElement()) {
    await exitFullscreen();
  } else if (canFullscreen()) {
    const ok = await enterFullscreen();
    if (!ok && isIPhone) { fullscreenHelp(); return; }
  } else if (isIPhone) {
    fullscreenHelp();
    return;
  }
  if (b) b.textContent = fsElement() ? "إنهاء ملء الشاشة" : "ملء الشاشة";
}

let fails = 0; // عدد المحاولات الفاشلة المتتالية (تُصفَّر عند اتصال دام أكثر من 3 ثوانٍ)
function connect() {
  if (!playing) return;
  closeOverlay();
  closeConn(); // لا نترك اتصالًا سابقًا مفتوحًا عند إعادة المحاولة
  setStatus(`P${slot} • جارٍ الاتصال…`);
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const mine = new WebSocket(`${proto}//${location.host}/ws?slot=${slot}`);
  mine.binaryType = "arraybuffer";
  ws = mine;
  let openedAt = 0, serverError = "";
  mine.onopen = () => { if (ws === mine) { openedAt = Date.now(); setStatus(`P${slot} • متصل`); dirty = true; } };
  mine.onmessage = (e) => {
    if (ws !== mine || typeof e.data !== "string") return;
    if (e.data === "replaced") {
      // جهاز آخر أخذ هذه الخانة: لا نستعيدها تلقائيًا (وإلا يتصارع الجهازان)
      ws = null; mine.close();
      overlay(`تم نقل P${slot} إلى جهاز آخر`, [["استعادة", connect], ["القائمة الرئيسية", backToMenu, true]]);
      return;
    }
    serverError = e.data; // الخادم رفض إنشاء الذراع، وهذا هو السبب الحقيقي (ViGEmBus، صلاحية uinput…)
  };
  mine.onclose = () => {
    if (ws !== mine) return;
    ws = null;
    if (serverError) {
      fails = 0;
      overlay("تعذّر إنشاء الذراع على الحاسوب:\n" + serverError, [["إعادة المحاولة", connect], ["القائمة الرئيسية", backToMenu, true]]);
      return;
    }
    fails = openedAt && Date.now() - openedAt > STABLE_MS ? 1 : fails + 1;
    if (fails >= MAX_RETRIES) {
      fails = 0;
      overlay("تعذّر الاتصال بالخادم.\nتأكد أنه يعمل وأن الهاتف على الشبكة نفسها.", [["إعادة المحاولة", connect], ["القائمة الرئيسية", backToMenu, true]]);
      return;
    }
    setStatus(`P${slot} • انقطع الاتصال… إعادة المحاولة`);
    setTimeout(connect, Math.min(RETRY_MS * fails, RETRY_CAP_MS));
  };
}

// عند إخفاء الصفحة (قفل الشاشة/تبديل التطبيق) صفّر كل شيء كي لا تبقى أزرار عالقة
document.addEventListener("visibilitychange", () => { if (document.hidden && playing) releaseAll(); });
addEventListener("pagehide", closeConn);
document.addEventListener("contextmenu", (e) => e.preventDefault());

/* =====================================================================
   القائمة الرئيسية
   ===================================================================== */
let layouts = [], layoutId = store.get("dopad.layout", "");
slot = Math.max(1, +store.get("dopad.slot", "1") || 1);
const playerChips = [];
let poll = null, lastBusy = [];

function stopPoll() { if (poll !== null) { clearInterval(poll); poll = null; } }
function startPoll() { stopPoll(); pollSlots(); poll = setInterval(pollSlots, POLL_MS); }

// يجلب JSON مع منع التخزين المؤقت ويرفض أي استجابة غير ناجحة.
async function getJSON(url) {
  const r = await fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error("HTTP " + r.status);
  return r.json();
}

// زر اختيار صغير (لاعب أو تخطيط) بنفس الشكل في القائمتين.
const chip = (label, onClick) => mkButton(label, "chip", onClick);

function renderPlayers(busy = lastBusy) {
  lastBusy = busy;
  if (playerChips.length !== busy.length) { // عدد اللاعبين يأتي من الخادم (DOPAD_PLAYERS)
    playersEl.innerHTML = ""; playerChips.length = 0;
    busy.forEach((_, i) => {
      const c = chip("P" + (i + 1), () => { slot = i + 1; store.set("dopad.slot", String(slot)); renderPlayers(); });
      playersEl.appendChild(c); playerChips.push(c);
    });
    if (slot > busy.length) { slot = 1; store.set("dopad.slot", "1"); }
  }
  playerChips.forEach((c, i) => { c.classList.toggle("sel", i + 1 === slot); c.classList.toggle("busy", !!busy[i]); });
}
async function pollSlots() {
  try { renderPlayers(await getJSON("/slots")); } catch (_) {}
}
function renderLayouts() {
  layoutsEl.innerHTML = "";
  if (!layouts.some((l) => l.id === layoutId)) layoutId = layouts.length ? layouts[0].id : "";
  for (const l of layouts) {
    const c = chip(l.name, () => { layoutId = l.id; store.set("dopad.layout", l.id); renderLayouts(); });
    c.classList.toggle("sel", l.id === layoutId);
    layoutsEl.appendChild(c);
  }
  if (!layouts.length) errEl.textContent = "لا توجد تخطيطات: ضع ملفات .json في مجلد layouts";
}
async function loadMenu() {
  try { layouts = await getJSON("/api/layouts"); } catch (_) { layouts = []; }
  renderLayouts();
  startPoll();
}

goEl.onclick = async () => {
  if (!layoutId) return;
  errEl.textContent = "";
  // ملء الشاشة يجب أن يُطلب داخل حدث اللمس نفسه، لذلك قبل أي await.
  // على iPhone لا يوجد Fullscreen API للصفحات؛ لا نحاول ولا نُظهر خطأ.
  const fs = canFullscreen() ? enterFullscreen() : Promise.resolve(false);
  let spec;
  try {
    spec = await getJSON(`/layouts/${encodeURIComponent(layoutId)}.json`);
  } catch (e) { errEl.textContent = "تعذر قراءة التخطيط: " + e.message; return; }
  await fs;
  stopPoll();
  build(spec);
  $("#menu").hidden = true; stage.hidden = false; statusEl.hidden = false;
  relayout(); // مواضع العصي الساكنة تُحسب بعد ظهور المنصة
  playing = true; releaseAll();
  connect();
};

function backToMenu() {
  playing = false; closeOverlay(); releaseAll(); closeConn();
  clearStage();
  stage.hidden = true; statusEl.hidden = true; $("#menu").hidden = false;
  startPoll();
}

// على iPhone داخل Safari: نذكّر المستخدم بالطريقة الوحيدة للشاشة الكاملة.
if (isIPhone && !isStandalone()) {
  const h = $("#fsHint");
  h.textContent = "📱 للشاشة الكاملة على iPhone: اضغط هنا لطريقة التثبيت على الشاشة الرئيسية";
  h.onclick = fullscreenHelp;
  h.hidden = false;
}

loadMenu();
