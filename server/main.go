// خادم الذراع (لينكس / ويندوز): WebSocket -> ذراع Xbox 360 افتراضية.
//
// على لينكس يُنشأ الجهاز عبر uinput + ioctl (انظر pad_linux.go).
// على ويندوز يُنشأ عبر برنامج تشغيل ViGEm Bus (انظر pad_windows.go).
// بلا cgo وبلا أي مكتبة خارجية — الواجهة المنصة-مستقلّة (HTTP، WebSocket،
// إدارة الفتحات — slots) تبقى هنا لكلا المنهجين.
//
// لماذا WebSocket وليس WebRTC؟  لأن Safari/Brave على iOS تتطلّبان
// سياق HTTPS لتفعيل RTCPeerConnection، وهو غير عملي للاستخدام المحلي
// دون تثبيت شهادة. WebSocket يعمل على HTTP على كل المتصفحات.
package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// envOr يقرأ المتغيّر البيئة name ويعيده إن كان غير فارغ، وإلا fallback.
// يُستعمل لتجاوز كشف الواجهة والمنفذ تلقائيًّا — مفيد جدًا عندما يصبح
// الحاسوب نقطة وصل مباشرة (hotspot بلا راوتر) ويختار كشف الواجهة عنوانًا غير الصحيح.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// ---------- خوادم الفتحات (slots) ----------
// كل خانة (P1..P6) ذراع افتراضية تبقى قائمة؛ الهاتف الذي يختارها يمتلكها،
// وإن اختارها هاتف آخر انتقلت إليه مع بقاء الذراع نفسها (لا تُفصل في اللعبة
// ولا يتغير ترقيمها).

type slot struct {
	pad  *Pad
	id   uint64  // معرّف الاتصال الحالي (0 = خالية)
	conn *wsConn // لإرسال "replaced" للهاتف القديم
}

var (
	slotMu      sync.Mutex
	slots       []slot // الطول = عدد اللاعبين (DOPAD_PLAYERS، الافتراضي 6)
	connCounter uint64 // عداد تزيده لكل اتصال جديد
)

// claim: خذ الخانة n لهذا الاتصال. تُنشأ P1..Pn بالترتيب حتى يطابق
// ترقيم الألعاب أرقام اللاعبين.
//
// لا نكتب للاتصال القديم أثناء إمساك slotMu: الكتابة قد تنتظر شبكة بطيئة
// فتُعطّل كل الاتصالات. نجمع بياناته ثم نكتب بعد فكّ القفل.
func claim(n int, id uint64, conn *wsConn) (*Pad, error) {
	slotMu.Lock()
	for i := 0; i <= n; i++ {
		if slots[i].pad == nil {
			p, err := newPad()
			if err != nil {
				slotMu.Unlock()
				return nil, err
			}
			slots[i].pad = p
		}
	}
	pad := slots[n].pad
	oldID, oldConn := slots[n].id, slots[n].conn
	slots[n].id, slots[n].conn = id, conn
	slotMu.Unlock()

	if oldID != 0 && oldID != id && oldConn != nil {
		_ = oldConn.write(wsText, []byte("replaced")) // نص للهاتف القديم
	}
	return pad, nil
}

func isCurrent(n int, id uint64) bool {
	slotMu.Lock()
	defer slotMu.Unlock()
	return slots[n].id == id
}

// release: إن كان هذا الاتصال ما زال صاحب الخانة، صفّر الذراع ثم حرّرها.
// التصفير يحدث تحت القفل كي لا يصل إلى المالك الجديد بعد أول حزمة يرسلها.
func release(n int, id uint64) {
	slotMu.Lock()
	defer slotMu.Unlock()
	if slots[n].id != id {
		return
	}
	slots[n].id, slots[n].conn = 0, nil
	if slots[n].pad != nil {
		slots[n].pad.apply(make([]byte, 8))
	}
}

// ---------- شبكة WebSocket (مكتبة معيارية فقط، لا dependencies) ----------

// wsGUID هو الثابت السحري المنصوص عليه في RFC 6455 §1.3. أي تغيير فيه
// يجعل Sec-WebSocket-Accept غير مطابق لما يتوقعه المتصفح، فيرفض المصافحة
// ويظهر للمستخدم «انقطع الاتصال» بلا سبب واضح (رمز الإغلاق 1006).
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// أنواع إطارات WebSocket المستعملة (RFC 6455 §5.2).
const (
	wsText   = 0x01
	wsBinary = 0x02
	wsClose  = 0x08
	wsPing   = 0x09
	wsPong   = 0x0A
)

// أقصى حجم إطار نقبله. بيانات اللعبة 8 بايت فقط، وأي إطار أكبر بكثير
// يدل على خطأ أو محاولة إغراق الذاكرة، لذا نرفضه قبل تخصيصه.
const maxFrameSize = 64 << 10

var errFrameTooLarge = errors.New("إطار WebSocket أكبر من الحد المسموح")

// wsConn يلفّ اتصال WebSocket بقفل كتابة واحد، لأن أكثر من goroutine قد
// يكتب لنفس الاتصال (رسالة "replaced" من اتصال جديد، ورد pong).
type wsConn struct {
	net.Conn
	mu sync.Mutex
}

func (c *wsConn) write(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := wsWriteFrame(c.Conn, opcode, payload)
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

// wsReadFrame يقرأ الإطار التالي من اتصال WebSocket.
// ملاحظة: الإطارات من العميل إلى الخادم مُعماة (masked).
func wsReadFrame(conn net.Conn) (opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(conn, hdr[:]); err != nil {
		return 0, nil, err
	}
	opcode = hdr[0] & 0x0F
	masked := hdr[1] & 0x80
	length := int64(hdr[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if length < 0 || length > maxFrameSize {
		return 0, nil, errFrameTooLarge
	}

	var mask [4]byte
	if masked != 0 {
		if _, err = io.ReadFull(conn, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(conn, payload); err != nil {
			return 0, nil, err
		}
		if masked != 0 {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
	}
	return opcode, payload, nil
}

// wsWriteFrame يكتب إطار WebSocket من الخادم إلى العميل (بدون أقنعة).
func wsWriteFrame(conn net.Conn, opcode byte, payload []byte) error {
	var h [10]byte
	h[0] = 0x80 | opcode
	n := 2
	switch l := len(payload); {
	case l < 126:
		h[1] = byte(l)
	case l < 1<<16:
		h[1] = 126
		binary.BigEndian.PutUint16(h[2:], uint16(l))
		n = 4
	default:
		h[1] = 127
		binary.BigEndian.PutUint64(h[2:], uint64(l))
		n = 10
	}
	_, err := conn.Write(append(h[:n], payload...))
	return err
}

// ---------- معالجات HTTP ----------

// writeBody يوحّد كتابة الردود الثنائية/النصية الصغيرة مع طول صحيح.
func writeBody(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// writeJSON يوحّد رؤوس ردود JSON ومنع التخزين المؤقت.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func slotsState(w http.ResponseWriter, _ *http.Request) {
	busy := make([]bool, len(slots))
	slotMu.Lock()
	for i := range slots {
		busy[i] = slots[i].id != 0
	}
	slotMu.Unlock()
	writeJSON(w, busy)
}

// manifestHandler يخدم Web App Manifest بنوع MIME الصحيح، لأن Go لا يعرف
// امتداد .webmanifest افتراضيًّا. هذا ما يسمح بتثبيت اللعبة كتطبيق ويب على
// الشاشة الرئيسية — وهو السبيل الوحيد للشاشة الكاملة على iPhone.
func manifestHandler(web string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(filepath.Join(web, "manifest.webmanifest"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeBody(w, "application/manifest+json", b)
	}
}

// wsFail يرسل سبب الرفض للهاتف ثم يغلق بلطف (الإغلاق المفاجئ قد يُسقط الرسالة قبل وصولها).
func wsFail(conn *wsConn, msg string) {
	_ = conn.write(wsText, []byte(msg))
	_ = conn.write(wsClose, nil)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	for {
		op, _, err := wsReadFrame(conn)
		if err != nil || op == wsClose {
			return
		}
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "ليس اتصال websocket", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "Sec-WebSocket-Key مفقود", http.StatusBadRequest)
		return
	}
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	w.Header().Set("Upgrade", "websocket")
	w.Header().Set("Connection", "Upgrade")
	w.Header().Set("Sec-WebSocket-Accept", accept)
	w.WriteHeader(http.StatusSwitchingProtocols)

	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	wc := &wsConn{Conn: conn}
	defer wc.Close()

	// معرف الفتحة من استعلام ?slot=N
	slotNum := 1
	if s := r.URL.Query().Get("slot"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(slots) {
			slotNum = n
		}
	}
	n := slotNum - 1

	id := atomic.AddUint64(&connCounter, 1)
	pad, err := claim(n, id, wc)
	if err != nil {
		log.Println(err)
		wsFail(wc, err.Error()) // يرسل السبب الحقيقي للهاتف ليعرضه بدل حلقة «جارٍ الاتصال»
		return
	}

	// حلقة قراءة: نتلقى 8 بايت كل 50 ملي ثانية من الهاتف
	wc.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer release(n, id)
	for {
		opcode, data, err := wsReadFrame(wc)
		if err != nil {
			break
		}
		wc.SetReadDeadline(time.Now().Add(10 * time.Second))
		if !isCurrent(n, id) {
			// اتصال جديد أخذ الخانة وأرسل "replaced"؛ نغلق القديم بلطف.
			// الكتابة آمنة لأن wsConn يقفل الاتصال ويرتّب الإطارات.
			_ = wc.write(wsClose, nil)
			break
		}
		switch opcode {
		case wsClose:
			_ = wc.write(wsClose, nil) // ردّ الإغلاق كما يفرضه البروتوكول
			return
		case wsPing:
			_ = wc.write(wsPong, data)
		case wsBinary:
			if len(data) == 8 {
				pad.apply(data)
			}
		}
	}
}

// ---------- الواجهة: ملفات عادية في مجلد web ----------

func findWeb() string {
	var c []string
	if d := envOr("DOPAD_WEB", ""); d != "" {
		c = append(c, d)
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		c = append(c, filepath.Join(d, "web"), filepath.Join(d, "..", "web"))
	}
	c = append(c, "web", filepath.Join("..", "web"))
	for _, d := range c {
		if st, err := os.Stat(filepath.Join(d, "index.html")); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(d); err == nil {
				return abs
			}
			return d
		}
	}
	return ""
}

func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// findLayouts يحدد مجلد التخطيطات بالترتيب: DOPAD_LAYOUTS، ثم layouts بجوار
// web (التنظيم الحالي)، ثم web/layouts للتوافق مع النسخ الأقدم.
func findLayouts(web string) string {
	if d := envOr("DOPAD_LAYOUTS", ""); d != "" {
		return d
	}
	outer := filepath.Join(filepath.Dir(web), "layouts")
	if st, err := os.Stat(outer); err == nil && st.IsDir() {
		return outer
	}
	return filepath.Join(web, "layouts")
}

func layoutsList(dir string) http.HandlerFunc {
	type info struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		list := []info{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, n))
			if err != nil {
				continue
			}
			var v struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(b, &v); err != nil {
				log.Printf("تخطيط معطوب %s: %v", n, err)
				continue
			}
			id := strings.TrimSuffix(n, ".json")
			if v.Name == "" {
				v.Name = id
			}
			list = append(list, info{id, v.Name})
		}
		writeJSON(w, list)
	}
}

// localIP يعيد عنوان IPv4 على شبكة LAN المحلية لعرضه على الهاتف.
//
// الاستراتيجية ثنائية:
//  1. نفس "حيلة الاتصال بلا إرسال": نربط UDP إلى عنوان غير موجه
//     (192.0.2.1 من RFC 5737 — لا يُرسل أي باقات، لأن connect() على UDP
//     يكتفي بحل مسار توجيه ويحدد عنوان المصدر). هذا يختار الواجهة التي
//     تستعملها جداول التوجيه — حتى لو لم يتوفر إنترنت، ما دام هناك راوتر/بوابة.
//  2. البديل: إذا لم يُعثر على مسار توجيه (ربط مباشر نقطي بلا بوابة مثل
//     نقطة اتصال الهاتف)، نعرّض الواجهات مباشرة ونأخذ أول عنوان محلي
//     غير الحلقي وغير مرتبط بالمؤقتات فقط.
//
// بهذه الطريقة يعمل الطباعة الصحيحة على الشبكة المحلية بلا إنترنت.
func localIP() string {
	if c, err := net.DialTimeout("udp", "192.0.2.1:1", time.Second); err == nil {
		defer c.Close()
		if ip := c.LocalAddr().(*net.UDPAddr).IP.To4(); ip != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		var fallback string
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.IsLinkLocalUnicast() {
				if fallback == "" {
					fallback = ip.String()
				}
				continue
			}
			return ip.String()
		}
		if fallback != "" {
			return fallback
		}
	}
	return "127.0.0.1"
}

func main() {
	var self bool
	flag.BoolVar(&self, "s", false, "self: use this computer itself as the WiFi hotspot (requires root)")
	var bt bool
	flag.BoolVar(&bt, "b", false, "bluetooth: اتصل بهاتف مقترن عبر البلوتوث بدل الواي فاي (PAN)")
	var fixFW bool
	flag.BoolVar(&fixFW, "fix-firewall", false, "(داخلي) ويندوز: أضف قاعدة سماح في جدار الحماية ثم اخرج")
	flag.Parse()
	if self && bt {
		fmt.Fprintln(os.Stderr, "لا يمكن الجمع بين -s (نقطة وصل واي فاي) و-b (بلوتوث): اختر واحداً.")
		os.Exit(2)
	}
	if fixFW { // النسخة المرفوعة المخفية التي يشغّلها ensureFirewall()
		if err := fixFirewall(); err != nil {
			os.Exit(1)
		}
		return
	}
	ensureFirewall()                    // ويندوز: إذن واحد (نعم) أول مرة بدل تعديل الجدار يدويًا؛ لا شيء على الأنظمة الأخرى
	if err := preflight(); err != nil { // فحص مبكر: يظهر السبب مرة واحدة هنا لا مع كل محاولة اتصال
		fmt.Fprintln(os.Stderr, "⚠️ ", err)
	}

	web := findWeb()
	if web == "" {
		fmt.Fprintln(os.Stderr, "لم أجد مجلد web (الذي فيه index.html). ضعه بجانب الملف التنفيذي أو حدّد DOPAD_WEB=/المسار")
		os.Exit(1)
	}
	players := 6
	if v, err := strconv.Atoi(os.Getenv("DOPAD_PLAYERS")); err == nil && v >= 1 && v <= 16 {
		players = v
	}
	slots = make([]slot, players)

	addr := envOr("DOPAD_ADDR", localIP())

	var stops []func()
	if self {
		a, fn, err := startSelfMode(envOr("DOPAD_SUBNET", "192.168.50"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		addr = a
		stops = append(stops, fn)
	}
	teardown := func() {
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
	}

	port := envOr("DOPAD_PORT", "8080")

	// Try to bind the port; if blocked (firewall/in-use), auto-fallback
	// to the next available port so non-technical users needn't intervene.
	fallback := false
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("⚠️ Port %s is not available. Trying other ports...\n", port)
		for p := 8081; p <= 8090; p++ {
			ln, err = net.Listen("tcp", fmt.Sprintf(":%d", p))
			if err == nil {
				port = strconv.Itoa(p)
				fallback = true
				break
			}
		}
		if err != nil {
			log.Fatal("No available port was found between 8080 and 8090. " +
				"Close other programs or select a port with DOPAD_PORT=<port>")
		}
	}

	// في الوضع العادي يكون العنوان معروفاً مسبقاً؛ في وضع البلوتوث يتغيّر
	// العنوان مع كل هاتف، فيبقى /qr يعرض أحدث رابط فقط.
	qrNow := &qrStore{}
	if !bt {
		qrNow.set(GenerateQR(fmt.Sprintf("http://%s:%s", addr, port)))
	}

	layouts := findLayouts(web)
	mux := http.NewServeMux()
	mux.Handle("/", noStore(http.FileServer(http.Dir(web))))
	mux.Handle("/layouts/", noStore(http.StripPrefix("/layouts/", http.FileServer(http.Dir(layouts)))))
	mux.HandleFunc("/api/layouts", layoutsList(layouts))
	mux.HandleFunc("/ws", wsHandler)
	mux.HandleFunc("/slots", slotsState)
	mux.HandleFunc("/manifest.webmanifest", manifestHandler(web))
	mux.HandleFunc("/qr", qrHandler(qrNow))

	if fallback {
		fmt.Printf("The port %s was selected automatically ✅ (the requested port is forbidden or occupied).\n", port)
	}
	fmt.Printf("\nInterface: %s, Players: %d\n\n", web, players)
	if bt {
		fmt.Printf("🟢 الخادم يعمل الآن على المنفذ %s — لا ينتظر أي هاتف.\n", port)
		fmt.Println("🔵 وضع البلوتوث مفعّل: سأطبع الرابط ورمز QR لحظة اتصال أول هاتف مقترن.")
		fmt.Println("   اقترن بالهاتف وفعّل «مشاركة الاتصال بالبلوتوث»؛ لا حاجة لإعادة التشغيل.")
	} else {
		fmt.Print(qrNow.get().ToASCII())
		fmt.Println()
		fmt.Printf("Open 🎮DOpad on browser: http://%s:%s\n", addr, port)
	}

	// مدير البلوتوث يعمل في الخلفية: يعيد المحاولة بلا نهاية ويعلن كل رابط
	// جديد. وصول صفر هاتف لا يوقف الخادم ولا يطبع أخطاء متكررة.
	if bt {
		stops = append(stops, startBTBackground(func(dev, ip string) {
			u := fmt.Sprintf("http://%s:%s", ip, port)
			q := GenerateQR(u)
			qrNow.set(q)
			fmt.Printf("\n✅ بلوتوث: %s — %s\n", dev, u)
			fmt.Print(q.ToASCII())
			fmt.Println()
		}))
	}

	// إعادة الوضع الأصلي عند Ctrl+C أو SIGTERM (نقطة الوصل/البلوتوث معاً).
	if len(stops) > 0 {
		sig := make(chan os.Signal, 4)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			teardown()
			os.Exit(0)
		}()
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // يمنع اتصالات Slowloris من احتجاز الخادم
		IdleTimeout:       60 * time.Second,
	}
	err = srv.Serve(ln)
	teardown()
	log.Fatal(err)
}

// qrStore يحمل أحدث رمز QR. في الوضع العادي لا يتغيّر أبداً، وفي وضع
// البلوتوث يُحدَّث عند كل هاتف يتصل كي يعرض /qr الرابط الصحيح.
type qrStore struct {
	mu sync.RWMutex
	qr *QRCode
}

func (s *qrStore) set(q *QRCode) {
	s.mu.Lock()
	s.qr = q
	s.mu.Unlock()
}

func (s *qrStore) get() *QRCode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.qr
}

// qrHandler يخدم أحدث رمز QR بتنسيق PNG عبر مسار /qr.
func qrHandler(store *qrStore) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		q := store.get()
		if q == nil {
			http.Error(w, "لم يتصل هاتف بعد — لا يوجد رمز QR حتى الآن", http.StatusNotFound)
			return
		}
		writeBody(w, "image/png", q.ToPNG(12))
	}
}
