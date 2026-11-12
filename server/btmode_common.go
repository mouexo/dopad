package main

// btmode_common.go — أجزاء وضع -b المشتركة بين لينكس وويندوز.
//
// وضع -b ينقل DOpad عبر البلوتوث بدل الواي فاي. المتصفح على الهاتف لا يملك
// واجهة مقبس بلوتوث، لذلك نستخدم PAN (BNEP): طبقة شبكة معيارية فوق البلوتوث
// تُنشئ واجهة شبكة وعليها IPv4، فيتعامل المتصفح مع http://<ip>:8080 كأي شبكة.
//
// الهاتف هو من يوزّع العنوان (NAP) والحاسوب عميل (PANU) — لأن iOS لا يملك دور
// PANU وويندوز لا يملك دور NAP؛ وهذا هو التقاطع الوحيد الذي يعمل على لينكس
// وويندوز وأندرويد وآيفون معاً بلا تطبيق على الهاتف.
//
// قاعدة التصميم: الخادم لا ينتظر أحداً. يعمل فوراً، ومدير البلوتوث يتصل
// بالهواتف المقترنة في الخلفية ويعيد المحاولة إلى ما لا نهاية، وكل هاتف يظهر
// يُعلَن رابطه. صفر هاتف = خادم يعمل بلا شكوى.

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// btProfilePrefix بادئة أسماء ملفات اتصال NetworkManager التي ينشئها DOpad.
const btProfilePrefix = "DOPad-BT-"

// ifaceIPv4 يعيد أول عنوان IPv4 عادي (غير loopback ولا link-local) للواجهة.
func ifaceIPv4(iface string) string {
	if iface == "" {
		return ""
	}
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := n.IP.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}
		return ip4.String()
	}
	return ""
}

// btProfileName اسم ملف الاتصال الخاص بهاتف معيّن (اتصال مستقل لكل هاتف،
// لكي تعمل عدة هواتف في وقت واحد).
func btProfileName(mac string) string {
	return btProfilePrefix + strings.ReplaceAll(strings.ToUpper(mac), ":", "")
}

// btWanted يقرأ DOPAD_BT ويتحقق من صيغته. فارغ = لا تفضيل (كل هاتف مقترن).
func btWanted() (mac string, ok bool, err error) {
	s := strings.TrimSpace(envOr("DOPAD_BT", ""))
	if s == "" {
		return "", false, nil
	}
	parts := strings.Split(strings.ReplaceAll(s, "-", ":"), ":")
	if len(parts) != 6 {
		return "", false, fmt.Errorf("عنوان بلوتوث غير صالح: %q (المطلوب AA:BB:CC:DD:EE:FF)", s)
	}
	for i, p := range parts {
		if len(p) != 2 {
			return "", false, fmt.Errorf("عنوان بلوتوث غير صالح: %q (المطلوب AA:BB:CC:DD:EE:FF)", s)
		}
		if _, e := strconv.ParseUint(p, 16, 8); e != nil {
			return "", false, fmt.Errorf("عنوان بلوتوث غير صالح: %q", s)
		}
		parts[i] = strings.ToUpper(p)
	}
	return strings.Join(parts, ":"), true, nil
}
