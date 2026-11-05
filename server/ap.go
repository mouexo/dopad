//go:build linux

// ap.go — وضع -s: الحاسوب نفسه يصبح نقطة وصل لاسلكية + خادم DHCP مدمج.
// (يحل محل local_ap.sh و dhcp.go. لا bash ولا iw ولا dnsmasq؛ فقط nmcli.)
//
// يعتمد هذا الملف على مكوّنات خاصة لينكس فقط (nmcli، ufw، Geteuid، SO_BINDTODEVICE،
// /run …)؛ لذا يُستبعَد من بناء ويندوز تمامًا. وضع -s على ويندوز غير مدعوم
// (انظر selfmode_windows.go).
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	apName    = "DOPad-AP"
	stateFile = "/run/dopad-ap.json"
)

type apState struct {
	UUID  string `json:"uuid"`  // اتصال الـ AP الذي أنشأناه
	Prev  string `json:"prev"`  // اتصال الواي فاي الأصلي قبل البدء
	Iface string `json:"iface"` // الواجهة اللاسلكية
	UFW   bool   `json:"ufw"`   // هل أضفنا قاعدة ufw
}

type accessPoint struct {
	IP   string
	st   apState
	dhcp *dhcpServer
	once sync.Once
}

// ---------- أدوات ----------

// run ينفّذ أمرًا خارجيًا ويعيد خرجه. المجموعة المستقلة تمنع Ctrl+C من قتله.
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%w: %s", err, s) // لا نطبع الوسائط حتى لا تظهر كلمة المرور
	}
	return s, nil
}

func nmcli(args ...string) (string, error) { return run("nmcli", args...) }

// nmcliRows ينفّذ nmcli بوضع terse ويعيد حقول كل سطر (الحقول تُطلب صراحةً).
func nmcliRows(fields string, args ...string) [][]string {
	out, _ := nmcli(append([]string{"-t", "-f", fields}, args...)...)
	var rows [][]string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			rows = append(rows, strings.Split(l, ":"))
		}
	}
	return rows
}

func detectWifi() string {
	for _, f := range nmcliRows("DEVICE,TYPE", "device") {
		if len(f) >= 2 && f[1] == "wifi" {
			return f[0]
		}
	}
	return ""
}

// activeOn يعيد UUID اتصال الواي فاي الفعّال حاليًا على الواجهة (أو "").
func activeOn(iface string) string {
	for _, f := range nmcliRows("UUID,TYPE,DEVICE", "connection", "show", "--active") {
		if len(f) == 3 && f[1] == "802-11-wireless" && f[2] == iface {
			return f[0]
		}
	}
	return ""
}

// removeConn ينزل الاتصال ويحذفه؛ يتجاهل الأخطاء لأن الاتصال قد يكون مفقودًا أصلًا.
func removeConn(uuid string) {
	if uuid == "" {
		return
	}
	nmcli("connection", "down", "uuid", uuid)
	nmcli("connection", "delete", "uuid", uuid)
}

// purgeStale يحذف بقايا اتصالات باسمنا الخاص فقط (DOPad-AP)، بالـ UUID.
func purgeStale() {
	for _, f := range nmcliRows("UUID,NAME", "connection", "show") {
		if len(f) == 2 && f[1] == apName {
			removeConn(f[0])
		}
	}
}

func hasAddr(iface, ip string) bool {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return false
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == ip {
			return true
		}
	}
	return false
}

// openUFW يسمح بكل الوارد على واجهة الـ AP فقط (DHCP والصفحة وWebSocket). يعيد true إن أضاف قاعدة.
func openUFW(iface string) bool {
	if _, err := exec.LookPath("ufw"); err != nil {
		return false
	}
	out, err := run("ufw", "status")
	if err != nil || !strings.Contains(out, "Status: active") || strings.Contains(out, " on "+iface) {
		return false
	}
	_, err = run("ufw", "allow", "in", "on", iface)
	return err == nil
}

func (a *accessPoint) save() {
	b, _ := json.Marshal(a.st)
	_ = os.WriteFile(stateFile, b, 0o600)
}

// restore يفكّ كل ما فعلناه ويعيد الواي فاي الأصلي. آمن للتكرار.
func restore(st apState) {
	removeConn(st.UUID)
	if st.UFW && st.Iface != "" {
		run("ufw", "delete", "allow", "in", "on", st.Iface)
	}
	if st.Prev != "" {
		if _, err := nmcli("-w", "20", "connection", "up", "uuid", st.Prev); err == nil {
			_ = os.Remove(stateFile)
			return
		}
	}
	if st.Iface != "" {
		nmcli("device", "connect", st.Iface)
	}
	_ = os.Remove(stateFile)
}

// ---------- بدء/إيقاف الـ AP ----------

var uuidRe = regexp.MustCompile(`\(([0-9a-fA-F-]{36})\)`)

func startAP(subnet string) (*accessPoint, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("يتطلب الجذر:  sudo ./DOpad -s")
	}
	if _, err := exec.LookPath("nmcli"); err != nil {
		return nil, errors.New("NetworkManager (nmcli) غير مثبت")
	}
	ssid := envOr("DOPAD_SSID", "DOPad")
	pass := envOr("DOPAD_PASS", "12345678")
	if len(pass) < 8 || len(pass) > 63 {
		return nil, fmt.Errorf("DOPAD_PASS يجب أن تكون 8–63 حرفًا (الآن %d)", len(pass))
	}
	iface := envOr("DOPAD_IFACE", "")
	if iface == "" {
		iface = detectWifi()
	}
	if iface == "" {
		return nil, errors.New("لم يُعثر على واجهة لاسلكية؛ عيّن DOPAD_IFACE=wlan0")
	}

	// إصلاح بقايا تشغيل سابق انقطع فجأة
	if b, err := os.ReadFile(stateFile); err == nil {
		var old apState
		if json.Unmarshal(b, &old) == nil {
			restore(old)
		}
	}
	purgeStale()

	a := &accessPoint{IP: subnet + ".1"}
	a.st = apState{Iface: iface, Prev: activeOn(iface)}

	out, err := nmcli("connection", "add", "type", "wifi", "ifname", iface, "con-name", apName,
		"autoconnect", "no",
		"wifi.ssid", ssid, "wifi.mode", "ap",
		"wifi.band", envOr("DOPAD_BAND", "bg"), "wifi.channel", envOr("DOPAD_CHANNEL", "6"),
		"wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", pass,
		"wifi-sec.proto", "rsn", "wifi-sec.pairwise", "ccmp", "wifi-sec.group", "ccmp",
		"ipv4.method", "manual", "ipv4.addresses", a.IP+"/24",
		"ipv4.never-default", "yes", "ipv4.ignore-auto-dns", "yes",
		"ipv6.method", "disabled",
		"connection.zone", "trusted")
	if err != nil {
		return nil, fmt.Errorf("فشل إنشاء اتصال الـ AP: %w", err)
	}
	m := uuidRe.FindStringSubmatch(out)
	if m == nil {
		return nil, fmt.Errorf("لم أستطع قراءة UUID من خرج nmcli: %q", out)
	}
	a.st.UUID = m[1]
	a.save()

	fail := func(e error) (*accessPoint, error) {
		restore(a.st)
		return nil, e
	}

	// بلا disconnect: NM يستبدل الاتصال القديم تلقائيًا على نفس الواجهة
	if _, err := nmcli("-w", "20", "connection", "up", "uuid", a.st.UUID, "ifname", iface); err != nil {
		return fail(fmt.Errorf("فشل تفعيل الـ AP (قد لا تدعم البطاقة وضع AP: جرّب  iw list | grep -A8 'Supported interface modes'): %w", err))
	}
	ready := false
	for i := 0; i < 40; i++ {
		if hasAddr(iface, a.IP) {
			ready = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		return fail(fmt.Errorf("لم يُحصَّص العنوان %s على %s", a.IP, iface))
	}

	a.st.UFW = openUFW(iface)
	a.save()

	d, err := startDHCP(iface, a.IP, 10, 50)
	if err != nil {
		return fail(err)
	}
	d.serve()
	a.dhcp = d
	return a, nil
}

// stop آمن للاستدعاء أكثر من مرة وأكثر من مكان.
func (a *accessPoint) stop() {
	a.once.Do(func() {
		fmt.Fprintln(os.Stderr, "\nجارٍ إيقاف نقطة الوصل وإعادة الواي فاي …")
		if a.dhcp != nil {
			a.dhcp.stop()
		}
		restore(a.st)
		fmt.Fprintln(os.Stderr, "تم.")
	})
}

// ---------- خادم DHCP مدمج ----------

type dhcpServer struct {
	conn   *net.UDPConn
	ap     net.IP
	prefix [3]byte
	lo, hi int
	mu     sync.Mutex
	leases map[[6]byte]int // MAC -> آخر خانة من العنوان
}

// startDHCP يربط المنفذ 67 بواجهة الـ AP فقط (SO_BINDTODEVICE).
// هذا مهم: بدونه تخرج الردود الإذاعية من الواجهة الخطأ (مثلاً الإيثرنت) ولا يصل شيء للهاتف.
func startDHCP(iface, apIP string, lo, hi int) (*dhcpServer, error) {
	ap := net.ParseIP(apIP).To4()
	if ap == nil {
		return nil, fmt.Errorf("عنوان AP غير صالح: %q", apIP)
	}
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			f := int(fd)
			serr = syscall.SetsockoptInt(f, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			if serr == nil {
				serr = syscall.SetsockoptInt(f, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
			}
			if serr == nil {
				serr = syscall.SetsockoptString(f, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
			}
		}); err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":67")
	if err != nil {
		return nil, fmt.Errorf("فشل ربط DHCP على :67 (%w) — هل dnsmasq أو خادم DHCP آخر يعمل؟", err)
	}
	s := &dhcpServer{conn: pc.(*net.UDPConn), ap: ap, lo: lo, hi: hi, leases: map[[6]byte]int{}}
	copy(s.prefix[:], ap[:3])
	return s, nil
}

func (s *dhcpServer) stop() { _ = s.conn.Close() }

func (s *dhcpServer) serve() {
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				return // أُغلق المقبس
			}
			if reply, dst := s.process(buf[:n]); reply != nil {
				_, _ = s.conn.WriteToUDP(reply, dst)
			}
		}
	}()
}

func (s *dhcpServer) ipAt(n int) net.IP {
	return net.IPv4(s.prefix[0], s.prefix[1], s.prefix[2], byte(n)).To4()
}

// hostOf يتحقق أن العنوان ضمن نطاقنا ويعيد خانته الأخيرة.
func (s *dhcpServer) hostOf(ip net.IP) (int, bool) {
	v := ip.To4()
	if v == nil || v[0] != s.prefix[0] || v[1] != s.prefix[1] || v[2] != s.prefix[2] {
		return 0, false
	}
	h := int(v[3])
	return h, h >= s.lo && h <= s.hi
}

// takenLocked يتحقق أن الخانة h محجوزة لجهاز آخر غير mac. يجب استدعاؤه تحت s.mu.
func (s *dhcpServer) takenLocked(mac [6]byte, h int) bool {
	for m, v := range s.leases {
		if v == h && m != mac {
			return true
		}
	}
	return false
}

func (s *dhcpServer) assign(mac [6]byte) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.leases[mac]; ok {
		return h
	}
	used := make(map[int]bool, len(s.leases)) // مجموعة العناوين المحجوزة: بحث خطّي واحد بدل تكرار المسح
	for _, h := range s.leases {
		used[h] = true
	}
	for h := s.lo; h <= s.hi; h++ {
		if !used[h] {
			s.leases[mac] = h
			return h
		}
	}
	return 0
}

func (s *dhcpServer) claim(mac [6]byte, h int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.takenLocked(mac, h) {
		return false
	}
	s.leases[mac] = h
	return true
}

func (s *dhcpServer) lookup(mac [6]byte) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leases[mac]
}

func (s *dhcpServer) process(p []byte) ([]byte, *net.UDPAddr) {
	if len(p) < 241 || p[0] != 1 || p[2] != 6 || binary.BigEndian.Uint32(p[236:240]) != 0x63825363 {
		return nil, nil
	}
	var mac [6]byte
	copy(mac[:], p[28:34])
	xid := p[4:8]
	flags := binary.BigEndian.Uint16(p[10:12])
	ciaddr := net.IP(p[12:16])
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	dst := bcast
	if !ciaddr.Equal(net.IPv4zero) && flags&0x8000 == 0 {
		dst = &net.UDPAddr{IP: append(net.IP{}, ciaddr...), Port: 68}
	}
	hw := net.HardwareAddr(mac[:])

	switch dhcpType(p) {
	case 1: // DISCOVER -> OFFER
		h := s.assign(mac)
		if h == 0 {
			return nil, nil
		}
		fmt.Printf("  DHCP %s  DISCOVER → OFFER %s\n", hw, s.ipAt(h))
		return s.reply(2, xid, flags, mac, h), bcast
	case 3: // REQUEST -> ACK / NAK
		if sid := optValue(p, 54); len(sid) == 4 && !net.IP(sid).Equal(s.ap) {
			return nil, nil // الطلب موجّه لخادم DHCP آخر
		}
		ip := net.IP(optValue(p, 50))
		if len(ip) != 4 {
			ip = ciaddr
			if ciaddr.Equal(net.IPv4zero) {
				if h := s.lookup(mac); h > 0 {
					ip = s.ipAt(h)
				}
			}
		}
		h, ok := s.hostOf(ip)
		if ok {
			ok = s.claim(mac, h)
		}
		if !ok {
			fmt.Printf("  DHCP %s  REQUEST %s → NAK\n", hw, ip)
			return s.reply(6, xid, flags, mac, 0), bcast
		}
		fmt.Printf("  DHCP %s  REQUEST → ACK %s\n", hw, s.ipAt(h))
		return s.reply(5, xid, flags, mac, h), dst
	}
	return nil, nil
}

func (s *dhcpServer) reply(msgType byte, xid []byte, flags uint16, mac [6]byte, host int) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 2, 1, 6 // BOOTREPLY, Ethernet, hlen
	copy(b[4:8], xid)
	binary.BigEndian.PutUint16(b[10:12], flags)
	if host > 0 {
		copy(b[16:20], s.ipAt(host)) // yiaddr
	}
	copy(b[20:24], s.ap) // siaddr
	copy(b[28:34], mac[:])
	binary.BigEndian.PutUint32(b[236:240], 0x63825363)

	b = append(b, 53, 1, msgType)
	b = append(b, 54, 4)
	b = append(b, s.ap...)
	if msgType != 6 {
		lease := make([]byte, 4)
		binary.BigEndian.PutUint32(lease, 12*3600) // Big-Endian (كان خطأً في النسخة السابقة)
		b = append(b, 1, 4, 255, 255, 255, 0)
		b = append(b, 3, 4)
		b = append(b, s.ap...)
		b = append(b, 6, 4)
		b = append(b, s.ap...)
		b = append(b, 51, 4)
		b = append(b, lease...)
	}
	return append(b, 255)
}

func dhcpType(p []byte) byte {
	if v := optValue(p, 53); len(v) > 0 {
		return v[0]
	}
	return 0
}

// optValue يقرأ خيارات DHCP بأمان (لا يتعطل مع حزمة مقطوعة).
func optValue(p []byte, t byte) []byte {
	if len(p) < 241 {
		return nil
	}
	o := p[240:]
	for len(o) > 0 {
		c := o[0]
		if c == 0 {
			o = o[1:]
			continue
		}
		if c == 255 || len(o) < 2 {
			return nil
		}
		l := int(o[1])
		if len(o) < 2+l {
			return nil
		}
		if c == t {
			return o[2 : 2+l]
		}
		o = o[2+l:]
	}
	return nil
}
