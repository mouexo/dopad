//go:build windows

// firewall_windows.go — يجعل أول تشغيل على ويندوز «نقرة نعم واحدة» بدل تعديل جدار الحماية يدويًا.
//
// المشكلة: جدار ويندوز يحجب الاتصالات الواردة لأي برنامج جديد. ونافذة «السماح» التي يعرضها ويندوز
// تسمح افتراضيًا بالشبكات الخاصة فقط (والواي فاي المنزلي كثيرًا ما يكون «عامًا»)، ولو ضُغط «إلغاء»
// يُنشأ حظر دائم. النتيجة: الهاتف يعلق على ERR_CONNECTION_TIMED_OUT.
//
// الحل: عند أول تشغيل نطلب صلاحية مسؤول مرة واحدة (نافذة UAC: نعم)، فتضيف نسخة مخفية من البرنامج
// قاعدة سماح لهذا البرنامج (كل أنواع الشبكات، وللأجهزة في الشبكة المحلية فقط) وتحذف أي قاعدة حظر سابقة له.
// بعدها لا يُطلب شيء: تُحفظ علامة في ProgramData مرتبطة بمسار البرنامج. ولا نقرأ مخرجات netsh
// (مترجمة حسب لغة ويندوز)، بل نعتمد على العلامة فقط.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const fwRule = "DOpad"

var shellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

func fwDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "DOpad")
}
func fwMarker() string  { return filepath.Join(fwDir(), "firewall.txt") }       // مسار البرنامج بعد النجاح
func fwErrFile() string { return filepath.Join(fwDir(), "firewall-error.txt") } // سبب الفشل

func selfExe() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func fwDone(exe string) bool {
	b, err := os.ReadFile(fwMarker())
	return err == nil && strings.EqualFold(strings.TrimSpace(string(b)), exe)
}

func netsh(args ...string) (string, error) {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// fixFirewall يُنفَّذ في النسخة المرفوعة (مسؤول): يحذف القواعد القديمة لهذا البرنامج ويضيف قاعدة سماح.
func fixFirewall() error {
	exe := selfExe()
	if exe == "" {
		return errors.New("تعذّر تحديد مسار البرنامج")
	}
	_ = os.MkdirAll(fwDir(), 0o755)
	_ = os.Remove(fwErrFile())

	// قواعد الحظر التي ينشئها ويندوز عند الضغط على «إلغاء» تتغلب على السماح، فنحذفها أولًا
	base := filepath.Base(exe)
	_, _ = netsh("advfirewall", "firewall", "delete", "rule", "name=all", "program="+exe)
	_, _ = netsh("advfirewall", "firewall", "delete", "rule", "name="+base)
	_, _ = netsh("advfirewall", "firewall", "delete", "rule", "name="+fwRule)

	out, err := netsh("advfirewall", "firewall", "add", "rule",
		"name="+fwRule, "dir=in", "action=allow", "program="+exe,
		"enable=yes", "profile=any", "remoteip=localsubnet")
	if err != nil {
		err = fmt.Errorf("%w: %s", err, out)
		_ = os.WriteFile(fwErrFile(), []byte(err.Error()), 0o644)
		return err
	}
	return os.WriteFile(fwMarker(), []byte(exe), 0o644)
}

// runElevated يشغّل البرنامج مخفيًا بصلاحية مسؤول (تظهر نافذة UAC). يعيد false إن رُفض الإذن.
func runElevated(exe, args string) bool {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(args)
	r, _, _ := shellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(params)),
		0, 0) // SW_HIDE
	return r > 32
}

// ensureFirewall يُستدعى قبل فتح المنفذ. لا يفعل شيئًا إن كان كل شيء جاهزًا من قبل.
func ensureFirewall() {
	exe := selfExe()
	if exe == "" || fwDone(exe) {
		return
	}
	fmt.Println("🛡️  أول تشغيل: سيطلب ويندوز إذنك مرة واحدة — اضغط «نعم» ليستطيع الهاتف الاتصال بالبرنامج.")
	start := time.Now()
	if !runElevated(exe, "--fix-firewall") {
		fmt.Println("⚠️  لم يُمنح الإذن، وقد لا يفتح الرابط على الهاتف.")
		fmt.Println("    أعد تشغيل البرنامج واضغط «نعم»، أو اضغط بزر الفأرة الأيمن على DOpad.exe ثم «تشغيل كمسؤول» مرة واحدة.")
		return
	}
	for i := 0; i < 180; i++ { // حتى 90 ثانية (وقت كافٍ لنافذة UAC)
		if fwDone(exe) {
			fmt.Println("✅ تم السماح للبرنامج في جدار الحماية. لن يُطلب هذا مرة أخرى.")
			return
		}
		if st, err := os.Stat(fwErrFile()); err == nil && st.ModTime().After(start) {
			b, _ := os.ReadFile(fwErrFile())
			fmt.Println("⚠️  تعذّر ضبط جدار الحماية تلقائيًا:", strings.TrimSpace(string(b)))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("⚠️  لم يكتمل ضبط جدار الحماية. إن لم يفتح الرابط على الهاتف فأعد تشغيل البرنامج واضغط «نعم».")
}
