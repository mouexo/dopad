// selfmode_linux.go — وضع -s (الحاسوب = نقطة وصل مباشرة). لينكس فقط.
//
// هذا الوضع يعتمد على nmcli/ufw/SO_BINDTODEVICE و /run — كلها خاصة لينكس.
// يُغلّف startSelfMode منطق ap.go (البدأ/الإيقاف/معالجة الإشارات).
//
//go:build linux

package main

import "fmt"

// selfAP يحمّله startSelfMode حتى يُوقفها معالجة الإشارة أو إغلاق الخادم.
var selfAP *accessPoint

// startSelfMode يهيئ الحاسوب نفسه كـ نقطة وصل لاسلكية (Hotspot)، يبدأ الخادم
// المدموج، ويعيد عنوانه. يُرجع teardown يُعيد الواي فاي الأصلي عند الاستدعاء.
// يُستدعى من main() عند تفعيل العلم -s.
func startSelfMode(subnet string) (addr string, teardown func(), err error) {
	ap, err := startAP(subnet)
	if err != nil {
		return "", nil, err
	}
	selfAP = ap

	// إشعار بكلمة المرور مرة واحدة (يدخل داخل بدء الـ AP بحيث لا يُطبوع مرتين).
	fmt.Printf("نقطة وصل مباشرة:  SSID=%s  كلمة المرور=%s\n",
		envOr("DOPAD_SSID", "DOPad"), envOr("DOPAD_PASS", "12345678"))

	teardown = func() {
		if selfAP != nil {
			selfAP.stop()
			selfAP = nil
		}
	}
	return ap.IP, teardown, nil
}
