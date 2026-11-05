//go:build windows

// selfmode_windows.go — وضع -s غير مدعوم على ويندوز.
//
// الوضع "-s" يعتمد على nmcli (NetworkManager) و ufw و /run و SO_BINDTODEVICE —
// كلها مكوّنات خاصة بلينكس. على ويندوز نرفضه صراحةً بدلاً من محاكاته بـ netsh
// (الـ hosted network مُعطّل على إصدارات Windows 10/11 الحديثة). شغّل DOpad
// عادياً على الشبكة المحلية بدلاً من ذلك.
package main

import "errors"

// startSelfMode يرفض وضع -s على ويندوز. شغّل DOpad عادياً على الشبكة المحلية.
func startSelfMode(_ string) (addr string, teardown func(), err error) {
	return "", func() {}, errors.New("وضع -s (نقطة وصل مباشرة) غير مدعوم على ويندوز؛ استخدم الشبكة المحلية العادية (افتح منه على الهاتف)")
}
