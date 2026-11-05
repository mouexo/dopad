// pad_windows.go — الخلفية Windows للذراع الافتراضية.
//
// على لينكس يستخدم DOpad جهاز uinput مباشرة، أما على ويندوز فلا يوجد uinput،
// لذا نستخدم برنامج تشغيل ViGEm Bus (https://github.com/ViGEm/ViGEmDriver) الذي
// يحاكي ذراع Xbox 360 حقيقي عبر ViGEmClient.dll.
//
// يتم تحميل الـ DLL مرة واحدة (Lazy عبر sync.Once) باستخدام الحزمة المعيارية
// syscall فقط — بلا cgo وبلا أي مكتبة خارجية.
//
// الملاحظة المميزة: كل البتات التي يرسلها الهاتف في uint16 d[4:6] تطابق تعريفات
// XINPUT تمامًا، لذا wButtons يُعيّن مباشرةً — لا حاجة لجدول تحويل الأزرار.
// أما المحاور (thumb) فتستخدم قيم بايت 0..255 (128 = مركز) بدلًا من 16 بت،
// ولا تحتاج إلى إنقاذ (-scale) كما في evdev على لينكس.
//
//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// XUSB_REPORT — التخطيط الذي تتطلّبه ViGEmClient (12 بايت بلا padding):
// أزرار 2 + زنادان 2 + أربعة محاور int16 (8). يُمرَّر بالمؤشّر لأنه أكبر من 8 بايت (قاعدة x64).
type xusbReport struct {
	wButtons      uint16 // بتات XINPUT مباشرة (DPAD + الأزرار + HOME…)
	bLeftTrigger  byte   // LT 0..255
	bRightTrigger byte   // RT 0..255
	sThumbLX      int16
	sThumbLY      int16
	sThumbRX      int16
	sThumbRY      int16
}

// axis16 يحوّل int8 من الهاتف (الأعلى موجب) إلى int16 لـ XInput (الأعلى موجب أيضًا، فلا نعكس).
func axis16(v int8) int16 {
	r := int32(v) * 32767 / 127
	if r < -32767 {
		r = -32767
	}
	return int16(r)
}

// Pad هو ذراع Xbox 360 افتراضية مدعومة عبر ViGEm على ويندوز.
type Pad struct {
	padBase
	client uintptr // VIGEM_CLIENT*
	target uintptr // VIGEM_TARGET*
}

// نتيجة النجاح في ViGEmClient هي 0x20000000 (وليست 0!). كل ما عداها خطأ.
const (
	vigemDLL = "ViGEmClient.dll"
	vigemOK  = 0x20000000

	vigemErrNoDriver   = 0xE0000001 // ViGEmBus غير مثبّت أو غير مفعّل
	vigemErrNoFreeSlot = 0xE0000002 // لا توجد خانة فارغة
	vigemErrVersion    = 0xE0000008 // عدم تطابق إصدار ViGEmBus مع الـ DLL
	vigemErrAccess     = 0xE0000009 // تعذّر الوصول لبرنامج التشغيل
)

var (
	errNoDLL  = errors.New("لم أجد " + vigemDLL + ". ضعه بجانب DOpad.exe (مثبّت برنامج تشغيل ViGEmBus لا يتضمنه). انظر README — قسم ويندوز")
	errBadDLL = errors.New(vigemDLL + " موجود لكنه لا يحتوي الدوال المطلوبة؛ استعمل نسخة x64 حديثة منه")
)

// vigemErr يترجم رمز الخطأ من ViGEmClient إلى رسالة مفهومة.
func vigemErr(op string, r uintptr) error {
	switch c := uint32(r); c {
	case vigemErrNoDriver:
		return fmt.Errorf("%s: برنامج تشغيل ViGEmBus غير مثبّت أو غير مفعّل — ثبّته من https://github.com/nefarius/ViGEmBus/releases ثم أعد تشغيل الحاسوب", op)
	case vigemErrNoFreeSlot:
		return fmt.Errorf("%s: لا توجد خانة فارغة في ViGEmBus (أغلق برامج الأذرع الافتراضية الأخرى)", op)
	case vigemErrVersion:
		return fmt.Errorf("%s: إصدار ViGEmBus لا يطابق %s — ثبّت أحدث إصدار من ViGEmBus", op, vigemDLL)
	case vigemErrAccess:
		return fmt.Errorf("%s: تعذّر الوصول لبرنامج التشغيل — أعد تشغيل الحاسوب بعد تثبيت ViGEmBus", op)
	default:
		return fmt.Errorf("%s: خطأ ViGEm 0x%X", op, c)
	}
}

// دوال ViGEm التي نحتاجها (تُحمَّل مرة واحدة).
var (
	vigemOnce    sync.Once
	vigemLoadErr error

	procVigemAlloc            *syscall.Proc
	procVigemConnect          *syscall.Proc
	procVigemDisconnect       *syscall.Proc
	procVigemFree             *syscall.Proc
	procVigemTargetX360Alloc  *syscall.Proc
	procVigemTargetX360Update *syscall.Proc
	procVigemTargetAdd        *syscall.Proc
	procVigemTargetFree       *syscall.Proc
	procVigemTargetSetVid     *syscall.Proc
	procVigemTargetSetPid     *syscall.Proc
)

// extractViGEm: إن بُني DOpad.exe بالوسم vigemdll فالـ DLL مضمّنة داخله، فنكتبها في مجلد TEMP ونحمّلها منه.
func extractViGEm() string {
	if len(embeddedViGEm) == 0 {
		return ""
	}
	dir := filepath.Join(os.TempDir(), "dopad-vigem")
	path := filepath.Join(dir, vigemDLL)
	if st, err := os.Stat(path); err == nil && st.Size() == int64(len(embeddedViGEm)) {
		return path
	}
	if os.MkdirAll(dir, 0o755) != nil || os.WriteFile(path, embeddedViGEm, 0o644) != nil {
		return ""
	}
	return path
}

// loadViGEmDLL يبحث عن ViGEmClient.dll: بجانب DOpad.exe، ثم المضمّنة، ثم المسارات العادية.
func loadViGEmDLL() (*syscall.DLL, error) {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), vigemDLL))
	}
	if p := extractViGEm(); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths, vigemDLL) // System32 / PATH / المجلد الحالي
	for _, p := range paths {
		if d, err := syscall.LoadDLL(p); err == nil {
			return d, nil
		}
	}
	return nil, errNoDLL
}

func loadViGEm() error {
	vigemOnce.Do(func() {
		d, err := loadViGEmDLL()
		if err != nil {
			vigemLoadErr = err
			return
		}
		specs := []struct {
			name string
			pp   **syscall.Proc
		}{
			{"vigem_alloc", &procVigemAlloc},
			{"vigem_connect", &procVigemConnect},
			{"vigem_disconnect", &procVigemDisconnect},
			{"vigem_free", &procVigemFree},
			{"vigem_target_x360_alloc", &procVigemTargetX360Alloc},
			{"vigem_target_x360_update", &procVigemTargetX360Update},
			{"vigem_target_add", &procVigemTargetAdd},
			{"vigem_target_free", &procVigemTargetFree},
			{"vigem_target_set_vid", &procVigemTargetSetVid},
			{"vigem_target_set_pid", &procVigemTargetSetPid},
		}
		for _, sp := range specs {
			p, e := d.FindProc(sp.name)
			if e != nil {
				vigemLoadErr = errBadDLL
				return
			}
			*sp.pp = p
		}
	})
	return vigemLoadErr
}

// allocClient يخصّص عميل ViGEm ويتصل بالبرنامج، ويعيد تنظيف نفسه عند الفشل.
func allocClient() (uintptr, error) {
	c, _, _ := procVigemAlloc.Call()
	if c == 0 {
		return 0, errors.New("تعذر تخصيص عميل ViGEm")
	}
	if res, _, _ := procVigemConnect.Call(c); uint32(res) != vigemOK {
		procVigemFree.Call(c)
		return 0, vigemErr("الاتصال ببرنامج تشغيل ViGEmBus", res)
	}
	return c, nil
}

func freeClient(c uintptr) {
	if c != 0 {
		procVigemDisconnect.Call(c)
		procVigemFree.Call(c)
	}
}

// allocTarget ينشئ ذراع Xbox 360 ويضيفها للعميل، ويحرّرها إن فشلت الإضافة.
func allocTarget(client uintptr) (uintptr, error) {
	t, _, _ := procVigemTargetX360Alloc.Call()
	if t == 0 {
		return 0, errors.New("تعذر تخصيص هدف Xbox 360 في ViGEm")
	}
	// يُبلّغ باسم Microsoft X-Box 360 pad (0x045e:0x028e) — يطابق إعدادات لينكس.
	procVigemTargetSetVid.Call(t, uintptr(x360Vendor))
	procVigemTargetSetPid.Call(t, uintptr(x360Product))
	if res, _, _ := procVigemTargetAdd.Call(client, t); uint32(res) != vigemOK {
		procVigemTargetFree.Call(t)
		return 0, vigemErr("إضافة الذراع", res)
	}
	return t, nil
}

func newPad() (*Pad, error) {
	if err := loadViGEm(); err != nil {
		return nil, err
	}
	client, err := allocClient()
	if err != nil {
		return nil, err
	}
	target, err := allocTarget(client)
	if err != nil {
		freeClient(client)
		return nil, err
	}
	return &Pad{client: client, target: target}, nil
}

// preflight: فحص مبكر عند التشغيل (DLL + برنامج التشغيل) كي يظهر السبب مرة واحدة بدل أن يتكرر مع كل اتصال.
func preflight() error {
	if err := loadViGEm(); err != nil {
		return err
	}
	client, err := allocClient()
	if err != nil {
		return err
	}
	freeClient(client)
	return nil
}

// 8 بايت: lx, ly, rx, ry (int8) | أزرار (uint16) | LT (uint8) | RT (uint8)
func (p *Pad) apply(d []byte) {
	if !p.begin(d) {
		return
	}
	defer p.end()
	if p.target == 0 {
		return
	}

	btn := binary.LittleEndian.Uint16(d[4:6])
	gp := xusbReport{
		wButtons:      btn, // كل البتات تطابق XINPUT تمامًا
		bLeftTrigger:  d[6],
		bRightTrigger: d[7],
		sThumbLX:      axis16(int8(d[0])),
		sThumbLY:      axis16(int8(d[1])), // الأعلى موجب في الهاتف وفي XInput: لا انعكاس
		sThumbRX:      axis16(int8(d[2])),
		sThumbRY:      axis16(int8(d[3])),
	}
	_, _, _ = procVigemTargetX360Update.Call(p.client, p.target, uintptr(unsafe.Pointer(&gp)))
}
