//go:build windows

// btmode_windows.go — وضع -b على ويندوز: البلوتوث بدل الواي فاي (PAN/BNEP).
//
// الهاتف هو NAP («مشاركة الاتصال بالبلوتوث» على أندرويد، «نقطة الاتصال
// الشخصية ← بلوتوث» على iOS) والحاسوب عميل PANU. ويندوز ينشئ واجهة شبكة
// بلوتوث («Bluetooth Network Connection») تحمل IPv4، فيفتح المتصفح العنوان.
//
// كل شيء هنا عبر syscall خام (bthprops.cpl و iphlpapi) — بلا cgo وبلا مكتبات.
//
// التصميم: الخادم يعمل فوراً ولا ينتظر هاتفاً. هذا الملف يدير البلوتوث في
// الخلفية: يعدّد الهواتف المقترنة، يجرّبها واحداً بعد الآخر، يعيد المحاولة
// بتراجع تدريجي بلا نهاية، ويعلن الرابط لحظة نجاحه. صفر هاتف = خادم يعمل
// بلا شكوى. ويندوز يسمح برابط PAN واحد في الوقت نفسه، لذا ندير رابطاً واحداً
// وننتقل إلى هاتف آخر تلقائياً إن فشل الأول.
//
// قواعد العزل (كما في لينكس):
//
//	R1 لا جسر بين واجهة البلوتوث وأي واجهة أخرى.
//	R2 نحذف المسار الافتراضي الذي يضيفه ويندوز على واجهة البلوتوث، كي يبقى
//	   الإنترنت على الواي فاي فقط ولا يتسرّب عبر الهاتف. حذف المسار يحتاج
//	   مسؤولاً؛ إن لم يتوفر نُحذّر صراحةً بدل الفشل الصامت.
//	R3 لا تعديل على إعدادات الواي فاي إطلاقاً.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	btServiceEnable  = 0x00000001
	btServiceDisable = 0x00000002

	afInet              = 2
	errorBufferOverflow = 111
	ifOperStatusUp      = 1

	gaaFlagSkipAnycast    = 0x0002
	gaaFlagSkipMulticast  = 0x0004
	gaaFlagSkipDNSServer  = 0x0008
	gaaFlagIncludeGateway = 0x0080
)

var (
	bthprops = syscall.NewLazyDLL("bthprops.cpl")
	iphlp    = syscall.NewLazyDLL("iphlpapi.dll")

	procBTFindFirstRadio  = bthprops.NewProc("BluetoothFindFirstRadio")
	procBTFindRadioClose  = bthprops.NewProc("BluetoothFindRadioClose")
	procBTFindFirstDevice = bthprops.NewProc("BluetoothFindFirstDevice")
	procBTFindNextDevice  = bthprops.NewProc("BluetoothFindNextDevice")
	procBTFindDeviceClose = bthprops.NewProc("BluetoothFindDeviceClose")
	procBTSetServiceState = bthprops.NewProc("BluetoothSetServiceState")

	procGetAdaptersAddresses = iphlp.NewProc("GetAdaptersAddresses")
)

// btGUIDNAP هو UUID خدمة Network Access Point على الهاتف (0x1116).
// Windows هو PANU ويتصل بخدمة NAP البعيدة.
var btGUIDNAP = syscall.GUID{
	Data1: 0x00001116,
	Data2: 0x0000,
	Data3: 0x1000,
	Data4: [8]byte{0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB},
}

// ---------- بنى Win32 ----------

type btFindRadioParams struct {
	dwSize uint32
}

// btDeviceInfo يطابق BLUETOOTH_DEVICE_INFO (560 بايت على x64).
type btDeviceInfo struct {
	dwSize          uint32
	Address         uint64
	ulClassofDevice uint32
	fConnected      int32
	fRemembered     int32
	fAuthenticated  int32
	stLastSeen      syscall.Systemtime
	stLastUsed      syscall.Systemtime
	szName          [248]uint16
}

type btDeviceSearchParams struct {
	dwSize               uint32
	fReturnAuthenticated int32
	fReturnRemembered    int32
	fReturnUnknown       int32
	fReturnConnected     int32
	fIssueInquiry        int32
	cTimeoutMultiplier   uint8
	_                    [3]byte
	hRadio               syscall.Handle
}

// rawSockaddr يمثل sockaddr/sockaddr_in (16 بايت).
type rawSockaddr struct {
	Family uint16
	Data   [14]byte
}

// unicastAddress يطابق IP_ADAPTER_UNICAST_ADDRESS_LH (نقرأ حقوله الأولى فقط).
type unicastAddress struct {
	Length             uint32
	Flags              uint32
	Next               *unicastAddress
	Address            rawSockaddr
	PrefixOrigin       uint32
	SuffixOrigin       uint32
	DadState           uint32
	ValidLifetime      uint32
	PreferredLifetime  uint32
	LeaseLifetime      uint32
	OnLinkPrefixLength uint8
}

type gatewayAddress struct {
	Length   uint32
	Reserved uint32
	Next     *gatewayAddress
	Address  rawSockaddr
}

// adapterAddresses يطابق بادئة IP_ADAPTER_ADDRESSES_LH (الحقول التي نقرأها فقط).
// القراءة فقط من مخزن يملؤه النظام، فقصر البنية آمن (لا نكتب فوقها).
type adapterAddresses struct {
	Length                 uint32
	IfIndex                uint32
	Next                   *adapterAddresses
	AdapterName            *byte
	FirstUnicastAddress    *unicastAddress
	FirstAnycastAddress    unsafe.Pointer
	FirstMulticastAddress  unsafe.Pointer
	FirstDNSServerAddress  unsafe.Pointer
	DNSSuffix              *uint16
	Description            *uint16
	FriendlyName           *uint16
	PhysicalAddress        [8]byte
	PhysicalAddressLength  uint32
	Flags                  uint32
	Mtu                    uint32
	IfType                 uint32
	OperStatus             uint32
	Ipv6IfIndex            uint32
	ZoneIndices            [16]uint32
	FirstPrefix            unsafe.Pointer
	TransmitLinkSpeed      uint64
	ReceiveLinkSpeed       uint64
	FirstWinsServerAddress unsafe.Pointer
	FirstGatewayAddress    *gatewayAddress
	Ipv4Metric             uint32
	Ipv6Metric             uint32
	Luid                   uint64
	Dhcpv4Server           rawSockaddr
}

// adapterInfo نتيجة مبسّطة لواجهة شبكة.
type adapterInfo struct {
	Name       string
	Desc       string
	IfIndex    uint32
	IfType     uint32
	OperStatus uint32
	IP         net.IP
	Gateway    net.IP
}

// winBTManager يدير رابط بلوتوث واحداً (ويندوز لا يسمح بأكثر من PAN في آن).
type winBTManager struct {
	want     uint64 // DOPAD_BT إن حُدِّد
	haveWant bool
	start    int // موضع البداية في قائمة الهواتف (دوران عادل)

	activeMAC  string
	activeName string
	ifIndex    uint32
	announced  bool
	routeDone  uint32
	routeWarn  bool
}

// ---------- بدء/إيقاف وضع البلوتوث ----------

// startBTBackground يبدأ مدير البلوتوث في الخلفية ويعيد دالة إنهاء.
// لا يحجب ولا يفشل: الخادم يعمل سواء وُجد هاتف أم لا.
func startBTBackground(onLink func(dev, ip string)) func() {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		fmt.Fprintln(os.Stderr, "⚠️  وضع -b على ويندوز مدعوم على الإصدارات 64-بت فقط.")
		return func() {}
	}
	if ref := envOr("DOPAD_BT_IFACE", ""); ref != "" {
		return monitorBTAdapter(ref, onLink)
	}

	var want uint64
	haveWant := false
	if s := strings.TrimSpace(envOr("DOPAD_BT", "")); s != "" {
		v, err := parseBTAddr(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, "⚠️ ", err)
			return func() {}
		}
		want, haveWant = v, true
	}

	m := &winBTManager{want: want, haveWant: haveWant}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.run(stop, onLink)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			select {
			case <-done:
			case <-time.After(4 * time.Second): // لا نُعلّق الإغلاق إن كان الاتصال معلّقاً
			}
			if m.activeMAC != "" {
				_ = btDisconnect(m.activeMAC)
			}
			fmt.Fprintln(os.Stderr, "🔵 تم قطع البلوتوث (الواي فاي لم يُمس).")
		})
	}
}

// monitorBTAdapter يراقب واجهة جاهزة يحدّدها المستخدم (نمط يدوي).
func monitorBTAdapter(ref string, onLink func(dev, ip string)) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	var cleared uint32
	announced, warned := false, false
	go func() {
		defer close(done)
		for {
			if stopped(stop) {
				return
			}
			if ad, ok := findAdapterRef(ref); ok {
				if cleared != ad.IfIndex {
					cleared = ad.IfIndex
					warned = false
					routeClear(ad, &warned)
				}
				if !announced {
					announced = true
					onLink(ref, ad.IP.String())
				}
			} else if announced {
				announced = false
				cleared = 0
				fmt.Fprintf(os.Stderr, "🔌 %s: انقطع الاتصال؛ أنتظر عودته.\n", ref)
			}
			if sleepOrStop(stop, 2*time.Second) {
				return
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			fmt.Fprintln(os.Stderr, "🔵 وضع البلوتوث: الواجهة الجاهزة لم تُمس.")
		})
	}
}

// routeClear يحذف المسار الافتراضي لواجهة البلوتوث (R2) ويحذر مرة واحدة.
func routeClear(ad adapterInfo, warned *bool) {
	if ad.Gateway == nil {
		return
	}
	if err := routeDelDefault(ad.Gateway, ad.IfIndex); err != nil {
		if !*warned {
			*warned = true
			fmt.Fprintln(os.Stderr, "⚠️  ويندوز قد يوجّه الإنترنت عبر البلوتوث (تعذّر حذف المسار بلا صلاحية مسؤول).")
			fmt.Fprintf(os.Stderr, "    لضمان بقاء الإنترنت على الواي فاي: شغّل DOpad كمسؤول مرة، أو نفّذ يدوياً:\n      route delete 0.0.0.0 mask 0.0.0.0 %s if %d\n", ad.Gateway, ad.IfIndex)
		}
	}
}

// ---------- حلقة المدير ----------

func (m *winBTManager) run(stop <-chan struct{}, onLink func(dev, ip string)) {
	backoff := 5 * time.Second
	warned := ""

	for {
		if stopped(stop) {
			return
		}

		// 1) هل الرابط الذي أنشأناه ما زال قائماً؟
		if ad, ok := m.currentLink(); ok {
			if m.routeDone != ad.IfIndex {
				m.routeDone = ad.IfIndex
				m.routeWarn = false
				routeClear(ad, &m.routeWarn)
			}
			if !m.announced {
				m.announced = true
				onLink(m.activeName, ad.IP.String())
			}
			backoff, warned = 5*time.Second, ""
			if sleepOrStop(stop, 2*time.Second) {
				return
			}
			continue
		}

		// 2) رابط موجود مسبقاً (اتصل المستخدم من إعدادات ويندوز) — نستخدمه.
		if ad, ok := findBTAdapter(); ok {
			if m.activeName == "" {
				m.activeName = "واجهة البلوتوث"
			}
			if !m.announced {
				m.routeDone = ad.IfIndex
				m.routeWarn = false
				routeClear(ad, &m.routeWarn)
				m.announced = true
				onLink(m.activeName, ad.IP.String())
			}
			if sleepOrStop(stop, 2*time.Second) {
				return
			}
			continue
		}

		// 3) كان هناك رابط وانقطع: نعلن ذلك مرة ونعيد المحاولة.
		if m.announced {
			m.announced = false
			m.ifIndex = 0
			m.routeDone = 0
			fmt.Fprintf(os.Stderr, "🔌 %s: انقطع الاتصال؛ سأعيد المحاولة تلقائياً.\n", m.activeName)
		}

		// 4) جرّب الهواتف المقترنة واحداً بعد الآخر.
		dev, err := m.tryPhones(stop)
		if err != nil {
			msg := err.Error()
			if warned != msg {
				fmt.Fprintf(os.Stderr, "⏳ %s\n", msg)
				warned = msg
			}
			if sleepOrStop(stop, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		m.activeMAC = btAddrString(dev.Address)
		m.activeName = deviceName(dev)
		fmt.Printf("🔵 بلوتوث: الهاتف %s (%s) — جارٍ تجهيز الشبكة ...\n", m.activeName, m.activeMAC)

		before := upIPv4Adapters()
		ad, err := waitBTAdapter(stop, before, 30*time.Second)
		if err != nil {
			msg := fmt.Sprintf("%s: %v", m.activeName, err)
			if warned != msg {
				fmt.Fprintf(os.Stderr, "⏳ %s\n", msg)
				warned = msg
			}
			if sleepOrStop(stop, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		m.ifIndex = ad.IfIndex
		m.routeDone = ad.IfIndex
		m.routeWarn = false
		routeClear(ad, &m.routeWarn)
		m.announced = true
		backoff, warned = 5*time.Second, ""
		onLink(m.activeName, ad.IP.String())
	}
}

// currentLink يعيد الرابط النشط إن كانت واجهته ما زالت تحمل IPv4.
func (m *winBTManager) currentLink() (adapterInfo, bool) {
	if m.ifIndex == 0 {
		return adapterInfo{}, false
	}
	list, err := collectAdapters()
	if err != nil {
		return adapterInfo{}, false
	}
	for _, a := range list {
		if a.IfIndex == m.ifIndex && a.IP != nil && a.OperStatus == ifOperStatusUp {
			return a, true
		}
	}
	return adapterInfo{}, false
}

// tryPhones يجرّب كل هاتف مقترن (بالدوران) حتى ينجح أحدها.
func (m *winBTManager) tryPhones(stop <-chan struct{}) (btDeviceInfo, error) {
	radio, find, err := openRadio()
	if err != nil {
		return btDeviceInfo{}, err
	}
	defer procBTFindRadioClose.Call(uintptr(find))
	defer syscall.CloseHandle(radio)

	devs, err := listBTDevices(radio)
	if err != nil {
		return btDeviceInfo{}, err
	}
	if len(devs) == 0 {
		return btDeviceInfo{}, errors.New("لا يوجد هاتف بلوتوث مقترن بعد. اقترن بالهاتف من إعدادات البلوتوث وسأتابع المحاولة تلقائياً")
	}

	var lastErr error
	tried := 0
	for i := 0; i < len(devs); i++ {
		if stopped(stop) {
			return btDeviceInfo{}, errors.New("أُوقف وضع البلوتوث")
		}
		d := devs[(m.start+i)%len(devs)]
		if m.haveWant && d.Address != m.want {
			continue
		}
		tried++
		if err := setService(radio, d, true); err != nil {
			lastErr = fmt.Errorf("%s: تعذّر تفعيل شبكة البلوتوث (%v). تأكد أن «مشاركة الاتصال بالبلوتوث» مفعّلة على الهاتف", deviceName(d), err)
			continue
		}
		m.start = m.start + i + 1
		return d, nil
	}
	if lastErr != nil {
		return btDeviceInfo{}, lastErr
	}
	if tried == 0 {
		return btDeviceInfo{}, fmt.Errorf("الهاتف المطلوب (%s) غير مقترن. اقترن به أولاً من إعدادات البلوتوث", btAddrString(m.want))
	}
	return btDeviceInfo{}, errors.New("تعذّر الاتصال بأي هاتف مقترن")
}

func nextBackoff(d time.Duration) time.Duration {
	if d < 10*time.Second {
		return 10 * time.Second
	}
	d *= 2
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

func stopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func sleepOrStop(stop <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-stop:
		return true
	case <-t.C:
		return false
	}
}

// ---------- Bluetooth API ----------

// openRadio يفتح أول محوّل بلوتوث. يعيد (مقبض المحوّل، مقبض البحث).
func openRadio() (syscall.Handle, syscall.Handle, error) {
	var p btFindRadioParams
	p.dwSize = uint32(unsafe.Sizeof(p))
	var radio syscall.Handle
	r, _, _ := procBTFindFirstRadio.Call(uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&radio)))
	if r == 0 {
		return 0, 0, errors.New("لا يوجد محوّل بلوتوث في هذا الحاسوب")
	}
	return radio, syscall.Handle(r), nil
}

// listBTDevices يعدّد الأجهزة المقترنة/المتصلة على المحوّل.
func listBTDevices(radio syscall.Handle) ([]btDeviceInfo, error) {
	var params btDeviceSearchParams
	params.dwSize = uint32(unsafe.Sizeof(params))
	params.fReturnAuthenticated = 1
	params.fReturnRemembered = 1
	params.fReturnConnected = 1
	params.hRadio = radio

	var info btDeviceInfo
	info.dwSize = uint32(unsafe.Sizeof(info))
	r, _, _ := procBTFindFirstDevice.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return nil, errors.New("لا يوجد هاتف بلوتوث مقترن بعد. اقترن بالهاتف من إعدادات البلوتوث وسأتابع المحاولة تلقائياً")
	}
	h := syscall.Handle(r)
	defer procBTFindDeviceClose.Call(uintptr(h))

	var out []btDeviceInfo
	for {
		out = append(out, info)
		info.dwSize = uint32(unsafe.Sizeof(info))
		if rr, _, _ := procBTFindNextDevice.Call(uintptr(h), uintptr(unsafe.Pointer(&info))); rr == 0 {
			return out, nil
		}
	}
}

// setService يفعّل/يعطّل خدمة NAP على جهاز بعينه.
func setService(radio syscall.Handle, dev btDeviceInfo, enable bool) error {
	action := uintptr(btServiceDisable)
	if enable {
		action = uintptr(btServiceEnable)
	}
	info := dev
	ret, _, _ := procBTSetServiceState.Call(
		uintptr(radio),
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Pointer(&btGUIDNAP)),
		action)
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

// btDisconnect يعطّل خدمة NAP (فيختفي المسار والواجهة تلقائياً).
func btDisconnect(mac string) error {
	want, err := parseBTAddr(mac)
	if err != nil {
		return err
	}
	radio, find, err := openRadio()
	if err != nil {
		return err
	}
	defer procBTFindRadioClose.Call(uintptr(find))
	defer syscall.CloseHandle(radio)

	devs, err := listBTDevices(radio)
	if err != nil {
		return err
	}
	for _, d := range devs {
		if d.Address == want {
			return setService(radio, d, false)
		}
	}
	return nil
}

// deviceName يعيد اسم الجهاز أو عنوانه إن كان الاسم فارغاً.
func deviceName(d btDeviceInfo) string {
	name := strings.TrimSpace(utf16ToString(d.szName[:]))
	if name == "" {
		return btAddrString(d.Address)
	}
	return name
}

// btAddrString يحوّل عنوان BLUETOOTH_ADDRESS إلى AA:BB:CC:DD:EE:FF.
func btAddrString(a uint64) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		byte(a>>40), byte(a>>32), byte(a>>24), byte(a>>16), byte(a>>8), byte(a))
}

// parseBTAddr يحوّل AA:BB:CC:DD:EE:FF إلى الصيغة التي يفهمها ويندوز.
func parseBTAddr(s string) (uint64, error) {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "-", ":"), ":")
	if len(parts) != 6 {
		return 0, fmt.Errorf("عنوان بلوتوث غير صالح: %q (المطلوب AA:BB:CC:DD:EE:FF)", s)
	}
	var a uint64
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return 0, fmt.Errorf("عنوان بلوتوث غير صالح: %q", s)
		}
		a |= v << (8 * uint(5-i))
	}
	return a, nil
}

// utf16ToString يحوّل مصفوفة UTF-16 منتهية بالصفر إلى نص Go.
func utf16ToString(b []uint16) string {
	for i, c := range b {
		if c == 0 {
			return syscall.UTF16ToString(b[:i])
		}
	}
	return syscall.UTF16ToString(b)
}

// ---------- واجهات الشبكة ----------

// collectAdapters يقرأ واجهات IPv4 مع بواباتها من GetAdaptersAddresses.
func collectAdapters() ([]adapterInfo, error) {
	size := uint32(16 << 10)
	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]byte, size)
		ret, _, _ := procGetAdaptersAddresses.Call(
			uintptr(afInet),
			uintptr(gaaFlagSkipAnycast|gaaFlagSkipMulticast|gaaFlagSkipDNSServer|gaaFlagIncludeGateway),
			0,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)))
		if ret == errorBufferOverflow {
			continue
		}
		if ret != 0 {
			return nil, fmt.Errorf("GetAdaptersAddresses: %v", syscall.Errno(ret))
		}

		var out []adapterInfo
		for p := (*adapterAddresses)(unsafe.Pointer(&buf[0])); p != nil; p = p.Next {
			if p.Length == 0 {
				break
			}
			ai := adapterInfo{
				Name:       utf16PtrToString(p.FriendlyName),
				Desc:       utf16PtrToString(p.Description),
				IfIndex:    p.IfIndex,
				IfType:     p.IfType,
				OperStatus: p.OperStatus,
			}
			for u := p.FirstUnicastAddress; u != nil; u = u.Next {
				if u.Address.Family == afInet {
					ai.IP = net.IPv4(u.Address.Data[2], u.Address.Data[3], u.Address.Data[4], u.Address.Data[5]).To4()
					break
				}
			}
			for g := p.FirstGatewayAddress; g != nil; g = g.Next {
				if g.Address.Family == afInet {
					ai.Gateway = net.IPv4(g.Address.Data[2], g.Address.Data[3], g.Address.Data[4], g.Address.Data[5]).To4()
					break
				}
			}
			out = append(out, ai)
		}
		return out, nil
	}
	return nil, errors.New("تعذّرت قراءة واجهات الشبكة")
}

// upIPv4Adapters يعيد فهارس الواجهات التي عليها IPv4 وفعّالة (لقطة قبل الاتصال).
func upIPv4Adapters() map[uint32]bool {
	m := map[uint32]bool{}
	list, err := collectAdapters()
	if err != nil {
		return m
	}
	for _, a := range list {
		if a.IP != nil && a.OperStatus == ifOperStatusUp {
			m[a.IfIndex] = true
		}
	}
	return m
}

// findBTAdapter يعيد واجهة بلوتوث فعّالة تحمل IPv4 (رابط موجود مسبقاً).
func findBTAdapter() (adapterInfo, bool) {
	list, err := collectAdapters()
	if err != nil {
		return adapterInfo{}, false
	}
	for _, a := range list {
		if a.IP == nil || a.OperStatus != ifOperStatusUp {
			continue
		}
		if strings.Contains(strings.ToLower(a.Name+" "+a.Desc), "bluetooth") {
			return a, true
		}
	}
	return adapterInfo{}, false
}

// findAdapterRef يعيد واجهة يحدّدها المستخدم بالاسم أو الوصف أو رقم الفهرس.
func findAdapterRef(ref string) (adapterInfo, bool) {
	list, err := collectAdapters()
	if err != nil {
		return adapterInfo{}, false
	}
	lref := strings.ToLower(ref)
	for _, a := range list {
		if a.IP == nil || a.OperStatus != ifOperStatusUp {
			continue
		}
		if strings.EqualFold(a.Name, ref) || strings.EqualFold(a.Desc, ref) ||
			strconv.FormatUint(uint64(a.IfIndex), 10) == ref ||
			strings.Contains(strings.ToLower(a.Name), lref) {
			return a, true
		}
	}
	return adapterInfo{}, false
}

// waitBTAdapter ينتظر ظهور واجهة البلوتوث مع عنوان.
// الأولوية لواجهة جديدة، ثم أي واجهة بلوتوث فعّالة.
func waitBTAdapter(stop <-chan struct{}, before map[uint32]bool, timeout time.Duration) (adapterInfo, error) {
	deadline := time.Now().Add(timeout)
	for {
		if stopped(stop) {
			return adapterInfo{}, errors.New("أُوقف وضع البلوتوث")
		}
		if list, err := collectAdapters(); err == nil {
			for _, a := range list {
				if a.IP != nil && a.OperStatus == ifOperStatusUp && !before[a.IfIndex] {
					return a, nil
				}
			}
			for _, a := range list {
				if a.IP == nil || a.OperStatus != ifOperStatusUp {
					continue
				}
				if strings.Contains(strings.ToLower(a.Name+" "+a.Desc), "bluetooth") {
					return a, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return adapterInfo{}, errors.New("لم تظهر واجهة شبكة البلوتوث — تأكد من تفعيل «مشاركة الاتصال بالبلوتوث» على الهاتف")
		}
		if sleepOrStop(stop, 500*time.Millisecond) {
			return adapterInfo{}, errors.New("أُوقف وضع البلوتوث")
		}
	}
}

// routeDelDefault يحذف المسار الافتراضي لواجهة البلوتوث فقط (R2).
func routeDelDefault(gw net.IP, ifIndex uint32) error {
	_, err := runWin("route", "delete", "0.0.0.0", "mask", "0.0.0.0",
		gw.String(), "if", strconv.FormatUint(uint64(ifIndex), 10))
	return err
}

// runWin ينفّذ أمراً ويندوزياً بلا نافذة.
func runWin(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%w: %s", err, s)
	}
	return s, nil
}

// utf16PtrToString يحوّل مؤشر UTF-16 منتهياً بالصفر إلى نص Go.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var n int
	for n < 1024 {
		if *(*uint16)(unsafe.Add(unsafe.Pointer(p), n*2)) == 0 {
			break
		}
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}
