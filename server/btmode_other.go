//go:build !linux && !windows

// btmode_other.go — وضع -b غير مدعوم على بقية الأنظمة.
//
// وضع البلوتوث يعتمد على NetworkManager/BlueZ على لينكس وعلى Winsock + Bluetooth
// APIs على ويندوز؛ ليس له مقابل على بقية الأنظمة. نرفضه صراحةً بدل الفشل الغامض،
// لكن الخادم يستمر في العمل على الشبكة العادية.

package main

import (
	"fmt"
	"os"
)

// startBTBackground يطبع سبب عدم الدعم ويعيد دالة إنهاء بلا مفعول.
func startBTBackground(onLink func(dev, ip string)) func() {
	fmt.Fprintln(os.Stderr, "⚠️  وضع -b (البلوتوث) مدعوم على لينكس وويندوز فقط؛ أتابع على الشبكة العادية.")
	return func() {}
}
