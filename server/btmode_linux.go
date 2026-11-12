//go:build linux

// btmode_linux.go — وضع -b على لينكس: البلوتوث بدل الواي فاي (PAN/BNEP).
//
// الهاتف هو NAP («مشاركة الاتصال بالبلوتوث» على أندرويد، «نقطة الاتصال
// الشخصية ← بلوتوث» على iOS) والحاسوب عميل PANU. النتيجة: واجهة شبكة تحمل
// عنوان IPv4 من الهاتف، فيفتح المتصفح http://<العنوان>:8080 بلا واي فاي.
//
// التصميم: الخادم يعمل فوراً ولا ينتظر هاتفاً. هذا الملف يدير البلوتوث في
// الخلفية: يكتشف الهواتف المقترنة، ينشئ لكل هاتف اتصالاً مستقلاً، يحاول
// باستمرار (بتراجع تدريجي)، ويعلن رابط كل هاتف عند نجاحه — بلا حد لعدد الهواتف.
//
// قواعد العزل (حتى لا يتصادم البلوتوث مع الواي فاي):
//
//	R1 لا جسر بين واجهة البلوتوث وأي واجهة أخرى.
//	R2 لا مسار افتراضي عبر البلوتوث (ipv4.never-default=yes) — الإنترنت يبقى
//	   على الواي فاي، والحاسوب لا يصبح راوتراً في أي اتجاه.
//	R3 لا تعديل على إعدادات الواي فاي إطلاقاً.
//
// المسار الافتراضي هو NetworkManager (بلا صلاحيات: المستخدم الجالس مخوَّل عبر
// polkit). إن لم يوجد NetworkManager (خادم بلا سطح مكتب) نرجع إلى نمط الجذر
// (BlueZ + عميل DHCP) ونقول ذلك صراحةً بدل الفشل الغامض.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------- أنواع مشتركة مع بقية الملفات ----------

// btDevice هاتف مقترن.
type btDevice struct {
	MAC  string // AA:BB:CC:DD:EE:FF
	Name string
}

// btLink حالة هاتف واحد داخل المدير.
type btLink struct {
	dev         btDevice
	profile     string
	state       string // idle | connecting | up
	ip          string
	cidr        string // مثال: 192.168.1.5/24
	iface       string
	ufw         bool
	backoff     time.Duration
	nextTry     time.Time
	warned      bool
	announced   bool
	clashWarned bool
}

// btManager يدير كل الهواتف في الخلفية.
type btManager struct {
	mu         sync.Mutex
	links      map[string]*btLink
	onLink     func(dev, ip string)
	want       string // DOPAD_BT (فارغ = كل الهواتف المقترنة)
	haveWant   bool
	warnedWant bool
}

// startBTBackground يبدأ مدير البلوتوث في الخلفية ويعيد دالة إنهاء.
// لا يحجب ولا يفشل: الخادم يعمل سواء وُجد هاتف أم لا.
func startBTBackground(onLink func(dev, ip string)) func() {
	if iface := envOr("DOPAD_BT_IFACE", ""); iface != "" {
		return startBTManual(iface, onLink)
	}
	want, haveWant, err := btWanted()
	if err != nil {
		fmt.Fprintln(os.Stderr, "⚠️ ", err)
		return func() {}
	}
	if !haveNM() {
		return startBTRoot(want, haveWant, onLink)
	}

	m := &btManager{links: map[string]*btLink{}, onLink: onLink, want: want, haveWant: haveWant}
	purgeBTProfiles()
	ctx, cancel := context.WithCancel(context.Background())
	go m.loop(ctx)
	return func() {
		cancel()
		m.shutdown()
	}
}

// startBTManual يراقب واجهة جاهزة يحدّدها المستخدم (نمط يدوي/جذر).
func startBTManual(iface string, onLink func(dev, ip string)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		announced := ""
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			ip := ifaceIPv4(iface)
			if ip != "" && ip != announced {
				announced = ip
				onLink("واجهة "+iface, ip)
			} else if ip == "" && announced != "" {
				announced = ""
				fmt.Fprintf(os.Stderr, "🔌 %s: انقطع الاتصال؛ أنتظر عودته.\n", iface)
			}
		}
	}()
	return cancel
}

// haveNM يتحقق من وجود nmcli وأن NetworkManager يعمل فعلاً.
func haveNM() bool {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return false
	}
	out, err := nmcli("-t", "-f", "RUNNING", "general")
	return err == nil && strings.Contains(out, "running")
}

// ---------- حلقة المدير ----------

func (m *btManager) loop(ctx context.Context) {
	m.scan()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	lastScan := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Since(lastScan) > 20*time.Second {
			m.scan()
			lastScan = time.Now()
		}
		m.tick(ctx)
	}
}

// scan يحدّث قائمة الهواتف المقترنة ويضيف الجديد منها (يحترم DOPAD_BT).
func (m *btManager) scan() {
	list, err := pairedBTDevices()
	if err != nil {
		return
	}
	m.mu.Lock()
	paired := map[string]bool{}
	added := false
	for _, d := range list {
		paired[d.MAC] = true
		if m.haveWant && d.MAC != m.want {
			continue
		}
		added = true
		if _, ok := m.links[d.MAC]; !ok {
			m.links[d.MAC] = &btLink{
				dev:     d,
				profile: btProfileName(d.MAC),
				state:   "idle",
			}
		}
	}
	// أزل من أُلغي اقترانه ولم يعد له رابط.
	for mac, l := range m.links {
		if l.state == "idle" && !paired[mac] {
			delete(m.links, mac)
		}
	}
	warn, message := false, ""
	if m.haveWant && !added && !m.warnedWant {
		warn = true
		m.warnedWant = true
		message = fmt.Sprintf("⏳ الهاتف المطلوب (%s) غير مقترن بعد؛ سأتابع المحاولة تلقائياً.", m.want)
	} else if added {
		m.warnedWant = false
	}
	m.mu.Unlock()

	if warn {
		fmt.Fprintln(os.Stderr, message)
		fmt.Fprintln(os.Stderr, "   اقترن به من إعدادات البلوتوث؛ لا حاجة لإعادة تشغيل DOpad.")
	}
}

// tick يبدأ محاولات الاتصال المستحقة ويتحقق من الروابط القائمة.
func (m *btManager) tick(ctx context.Context) {
	now := time.Now()
	m.mu.Lock()
	var toConnect, toCheck []*btLink
	for _, l := range m.links {
		switch l.state {
		case "idle":
			if !now.Before(l.nextTry) {
				l.state = "connecting"
				toConnect = append(toConnect, l)
			}
		case "up":
			toCheck = append(toCheck, l)
		}
	}
	m.mu.Unlock()

	for _, l := range toConnect {
		go m.connect(ctx, l)
	}
	for _, l := range toCheck {
		m.check(l)
	}
}

// connect محاولة واحدة لربط هاتف واحد.
func (m *btManager) connect(ctx context.Context, l *btLink) {
	if err := m.ensureProfile(l); err != nil {
		m.fail(l, err)
		return
	}

	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if _, err := runCtx(cctx, "nmcli", "-w", "30", "connection", "up", l.profile); err != nil {
		m.fail(l, err)
		return
	}

	addr := nmcliValue(l.profile, "IP4.ADDRESS")
	iface := nmcliValue(l.profile, "GENERAL.IP-IFACE")
	if addr == "" || iface == "" {
		m.fail(l, errors.New("الاتصال نجح لكن لم يصل عنوان IPv4"))
		m.drop(l)
		return
	}
	ip := strings.SplitN(addr, "/", 2)[0]

	// R2: تنبيه (لا إيقاف) لو صار البلوتوث مساراً افتراضياً.
	if hasDefaultDev(iface) {
		fmt.Fprintf(os.Stderr, "⚠️  %s: واجهة البلوتوث (%s) تحمل مساراً افتراضياً. الإنترنت قد يتسرّب عبر الهاتف.\n", l.dev.Name, iface)
		if os.Geteuid() == 0 {
			run("ip", "route", "del", "default", "dev", iface)
		}
	}

	added, warn := allowUFW(iface)
	if warn != "" {
		fmt.Fprintln(os.Stderr, "⚠️ ", warn)
	}

	// تعارض الشبكات الفرعية: آيفونان يستخدمان 172.20.10.0/28 معاً، فلا يمكن
	// التمييز بينهما. نُبقي الأول ونحذر بدل إخفاء المشكلة.
	if other := m.subnetClash(l, addr); other != "" {
		m.mu.Lock()
		clash := !l.clashWarned
		l.clashWarned = true
		m.mu.Unlock()
		if clash {
			fmt.Fprintf(os.Stderr, "⚠️  %s: شبكة البلوتوث %s تتعارض مع %s. قد يعمل رابط واحد فقط من الهاتفين في وقت واحد.\n", l.dev.Name, addr, other)
		}
	}

	m.mu.Lock()
	l.state, l.ip, l.cidr, l.iface, l.ufw = "up", ip, addr, iface, added
	l.backoff, l.warned = 0, false
	first := !l.announced
	l.announced = true
	m.mu.Unlock()

	if first {
		m.onLink(l.dev.Name, ip)
	} else {
		fmt.Printf("✅ %s: عاد الاتصال — %s\n", l.dev.Name, ip)
	}
}

// subnetClash يعيد وصف رابط قائم يتشارك نفس الشبكة الفرعية (أو "").
func (m *btManager) subnetClash(self *btLink, cidr string) string {
	_, selfNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.links {
		if o == self || o.state != "up" || o.cidr == "" {
			continue
		}
		if _, oNet, err := net.ParseCIDR(o.cidr); err == nil && selfNet.String() == oNet.String() {
			return fmt.Sprintf("%s (%s)", o.dev.Name, o.cidr)
		}
	}
	return ""
}

// check يتحقق أن الرابط ما زال قائماً، ويعيد المحاولة إن انقطع.
func (m *btManager) check(l *btLink) {
	if nmcliValue(l.profile, "GENERAL.STATE") == "activated" && ifaceIPv4(l.iface) != "" {
		return
	}
	m.drop(l)
	fmt.Fprintf(os.Stderr, "🔌 %s: انقطع الاتصال؛ سأعيد المحاولة تلقائياً.\n", l.dev.Name)
}

// drop ينظّف رابطاً منقطعاً ويعيد حالته إلى الانتظار.
func (m *btManager) drop(l *btLink) {
	m.mu.Lock()
	ufw, iface := l.ufw, l.iface
	l.state, l.ip, l.cidr, l.iface, l.ufw = "idle", "", "", "", false
	l.announced, l.warned = false, false
	l.backoff = 5 * time.Second
	l.nextTry = time.Now().Add(l.backoff)
	m.mu.Unlock()

	if ufw && iface != "" {
		run("ufw", "delete", "allow", "in", "on", iface)
	}
	run("nmcli", "connection", "down", l.profile)
}

// fail يطبّق تراجعاً تدريجياً (5s → 60s) ويطبع سبباً مرة واحدة لكل محاولة فاشلة.
func (m *btManager) fail(l *btLink, err error) {
	m.mu.Lock()
	l.state = "idle"
	if l.backoff == 0 {
		l.backoff = 5 * time.Second
	} else if l.backoff < 60*time.Second {
		l.backoff *= 2
		if l.backoff > 60*time.Second {
			l.backoff = 60 * time.Second
		}
	}
	l.nextTry = time.Now().Add(l.backoff)
	warn := !l.warned
	l.warned = true
	m.mu.Unlock()

	if warn {
		fmt.Fprintf(os.Stderr, "⏳ %s: لم أستطع الاتصال بعد؛ سأعيد المحاولة تلقائياً.\n", l.dev.Name)
		fmt.Fprintf(os.Stderr, "   تأكد من تفعيل «مشاركة الاتصال بالبلوتوث» على الهاتف. (السبب: %v)\n", err)
	}
}

// ensureProfile ينشئ ملف اتصال NetworkManager لهذا الهاتف إن لم يكن موجوداً.
func (m *btManager) ensureProfile(l *btLink) error {
	for _, f := range nmcliRows("NAME", "connection", "show") {
		if len(f) == 1 && f[0] == l.profile {
			return nil
		}
	}
	if _, err := nmcli("connection", "add", "type", "bluetooth", "con-name", l.profile,
		"bluetooth.type", "panu", "bluetooth.bdaddr", l.dev.MAC,
		"ipv4.method", "auto",
		"ipv4.never-default", "yes", // R2: لا مسار افتراضي عبر البلوتوث
		"ipv4.ignore-auto-dns", "yes",
		"ipv6.method", "ignore", // لا نلمس IPv6 إطلاقاً
		"connection.autoconnect", "no",
	); err != nil {
		return fmt.Errorf("تعذّر إنشاء اتصال بلوتوث (%w). إن كان السبب صلاحيات، أضف المستخدم إلى مجموعة network أو شغّل: sudo ./DOpad -b", err)
	}
	return nil
}

// shutdown ينزل ويحذف كل ملفات اتصال أنشأناها، ويزيل قواعد ufw.
func (m *btManager) shutdown() {
	time.Sleep(500 * time.Millisecond) // ندع أي محاولة جارية تنتهي
	m.mu.Lock()
	links := make([]*btLink, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, l)
	}
	m.mu.Unlock()

	for _, l := range links {
		if l.ufw && l.iface != "" {
			run("ufw", "delete", "allow", "in", "on", l.iface)
		}
		run("nmcli", "connection", "down", l.profile)
		run("nmcli", "connection", "delete", l.profile)
	}
	if len(links) == 0 {
		fmt.Fprintln(os.Stderr, "🔵 أُوقف وضع البلوتوث (لم يكن هناك رابط، والواي فاي لم يُمس).")
		return
	}
	fmt.Fprintln(os.Stderr, "🔵 تم قطع البلوتوث وحذف إعداداته (الواي فاي لم يُمس).")
}

// purgeBTProfiles يحذف بقايا تشغيل سابق انقطع فجأة (أسماء DOPad-BT-* فقط).
func purgeBTProfiles() {
	for _, f := range nmcliRows("UUID,NAME", "connection", "show") {
		if len(f) == 2 && strings.HasPrefix(f[1], btProfilePrefix) {
			nmcli("connection", "down", "uuid", f[0])
			nmcli("connection", "delete", "uuid", f[0])
		}
	}
}

// ---------- أدوات nmcli ----------

// nmcliValue يقرأ قيمة حقل واحد من ملف اتصال (أول سطر غير فارغ).
func nmcliValue(profile, field string) string {
	out, err := run("nmcli", "-g", field, "connection", "show", profile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// runCtx ينفّذ أمراً مع سياق (للإلغاء والمهلة).
func runCtx(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%w: %s", err, s)
	}
	return s, nil
}

// ---------- الهواتف المقترنة والمسارات ----------

// pairedBTDevices يقرأ الأجهزة المقترنة من BlueZ.
func pairedBTDevices() ([]btDevice, error) {
	if _, err := exec.LookPath("bluetoothctl"); err != nil {
		return nil, errors.New("bluetoothctl غير موجود (حزمة bluez)")
	}
	out, err := run("bluetoothctl", "devices", "Paired")
	if err != nil {
		return nil, fmt.Errorf("تعذّر قراءة الأجهزة المقترنة: %w", err)
	}
	var list []btDevice
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "Device" {
			continue
		}
		name := ""
		if len(f) > 2 {
			name = strings.Join(f[2:], " ")
		}
		list = append(list, btDevice{MAC: strings.ToUpper(f[1]), Name: name})
	}
	return list, nil
}

// defaultRouteDevs يعيد واجهات كل المسارات الافتراضية الحالية.
func defaultRouteDevs() []string {
	out, err := run("ip", "-o", "route", "show", "default")
	if err != nil {
		return nil
	}
	var devs []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "dev" {
				devs = append(devs, f[i+1])
				break
			}
		}
	}
	return devs
}

// hasDefaultDev يتحقق هل الواجهة تحمل أي مسار افتراضي.
func hasDefaultDev(iface string) bool {
	for _, d := range defaultRouteDevs() {
		if d == iface {
			return true
		}
	}
	return false
}

// allowUFW يسمح بالوارد على واجهة البلوتوث فقط إن كان ufw فعّالاً.
func allowUFW(iface string) (bool, string) {
	if _, err := exec.LookPath("ufw"); err != nil {
		return false, ""
	}
	out, err := run("ufw", "status")
	if err != nil || !strings.Contains(out, "Status: active") {
		return false, ""
	}
	if strings.Contains(out, " on "+iface) {
		return false, ""
	}
	if _, err := run("ufw", "allow", "in", "on", iface); err != nil {
		return false, fmt.Sprintf("جدار ufw فعّال وقد يمنع الهاتف. اسمح بالواجهة يدوياً:  sudo ufw allow in on %s", iface)
	}
	return true, ""
}

// ---------- نمط الجذر (NetworkManager غير موجود) ----------

// startBTRoot يحاول ربط الهواتف عبر BlueZ مباشرة (bt-network) + DHCP.
// يعمل في الخلفية أيضاً، ولا يوقف الخادم إن فشل.
func startBTRoot(want string, haveWant bool, onLink func(dev, ip string)) func() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "⚠️  NetworkManager غير موجود؛ وضع البلوتوث يحتاج صلاحية:  sudo ./DOpad -b")
		fmt.Fprintln(os.Stderr, "   أو ثبّت NetworkManager ليعمل بلا صلاحيات.")
		return func() {}
	}
	if _, err := exec.LookPath("bt-network"); err != nil {
		fmt.Fprintln(os.Stderr, "⚠️  NetworkManager غير موجود ولا يوجد bt-network (حزمة bluez-tools).")
		fmt.Fprintln(os.Stderr, "   ثبّت أحدهما ثم أعد التشغيل.")
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	type rootLink struct {
		cmd   *exec.Cmd
		iface string
		name  string
	}
	active := map[string]*rootLink{} // mac -> bt-network + واجهته
	announced := map[string]string{} // mac -> آخر عنوان أُعلن
	warnedWant := false

	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}

			// 1) راقب الروابط القائمة: جدّد DHCP عند فقد العنوان، وأزل المنقطع.
			mu.Lock()
			for mac, l := range active {
				if ifaceIPv4(l.iface) == "" {
					_ = dhcpOnce(l.iface)
				}
				ip := ifaceIPv4(l.iface)
				if ip == "" {
					if l.cmd.Process != nil {
						_ = syscall.Kill(-l.cmd.Process.Pid, syscall.SIGTERM)
					}
					delete(active, mac)
					delete(announced, mac)
					fmt.Fprintf(os.Stderr, "🔌 %s: انقطع الاتصال؛ سأعيد المحاولة تلقائياً.\n", l.name)
					continue
				}
				if announced[mac] != ip {
					announced[mac] = ip
					go onLink(l.name, ip)
				}
			}
			mu.Unlock()

			// 2) ابدأ روابط للهواتف المقترنة التي لا رابط لها.
			list, err := pairedBTDevices()
			if err != nil {
				continue
			}
			matched := false
			for _, dev := range list {
				if haveWant && dev.MAC != want {
					continue
				}
				matched = true
				mu.Lock()
				_, busy := active[dev.MAC]
				mu.Unlock()
				if busy {
					continue
				}
				before := bnepInterfaces()
				cmd := exec.Command("bt-network", "-c", dev.MAC, "nap")
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := cmd.Start(); err != nil {
					continue
				}

				// انتظر ظهور واجهة BNEP جديدة (لا تفترض الاسم bnep0).
				iface := ""
				for i := 0; i < 60 && iface == "" && ctx.Err() == nil; i++ {
					for n := range bnepInterfaces() {
						if !before[n] {
							iface = n
							break
						}
					}
					if iface == "" {
						time.Sleep(250 * time.Millisecond)
					}
				}
				ok := false
				if iface != "" && dhcpOnce(iface) == nil {
					if ip := ifaceIPv4(iface); ip != "" {
						mu.Lock()
						active[dev.MAC] = &rootLink{cmd: cmd, iface: iface, name: dev.Name}
						announced[dev.MAC] = ip
						mu.Unlock()
						onLink(dev.Name, ip)
						ok = true
					}
				}
				if ok {
					continue
				}
				if cmd.Process != nil {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				}
				_, _ = cmd.Process.Wait()
			}
			if haveWant && !matched && !warnedWant {
				warnedWant = true
				fmt.Fprintf(os.Stderr, "⏳ الهاتف المطلوب (%s) غير مقترن بعد؛ سأتابع المحاولة تلقائياً.\n", want)
			} else if matched {
				warnedWant = false
			}
		}
	}()

	return func() {
		cancel()
		mu.Lock()
		for _, l := range active {
			if l.cmd.Process != nil {
				_ = syscall.Kill(-l.cmd.Process.Pid, syscall.SIGTERM)
				_, _ = l.cmd.Process.Wait()
			}
		}
		mu.Unlock()
		fmt.Fprintln(os.Stderr, "🔵 تم قطع البلوتوث (الواي فاي لم يُمس).")
	}
}

// dhcpOnce يشغّل عميل DHCP متاحاً لمرة واحدة على الواجهة.
func dhcpOnce(iface string) error {
	if _, err := exec.LookPath("dhcpcd"); err == nil {
		if _, err := run("dhcpcd", "-4", "-1", "-t", "15", iface); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("dhclient"); err == nil {
		if _, err := run("dhclient", "-4", "-1", iface); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("udhcpc"); err == nil {
		if _, err := run("udhcpc", "-i", iface, "-n", "-q", "-t", "8"); err == nil {
			return nil
		}
	}
	return errors.New("لا يوجد عميل DHCP (dhcpcd أو dhclient أو udhcpc) أو فشل الحصول على عنوان")
}

// bnepInterfaces يعيد أسماء واجهات BNEP الحالية.
func bnepInterfaces() map[string]bool {
	m := map[string]bool{}
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if strings.HasPrefix(i.Name, "bnep") {
			m[i.Name] = true
		}
	}
	return m
}
