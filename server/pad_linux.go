// pad_linux.go — الخلفية Linux للذراع الافتراضية.
//
// على لينكس، DOpad ينشئ جهاز uinput افتراضي (ذراع Xbox 360) مباشرة عبر ioctl
// على /dev/uinput. لا cgo ولا مكتبات خارجية — فقط syscall المعيارية.
//
// الواجهة المشتركة التي يطلبها main.go:
//   newPad() (*Pad, error)   — يفتح /dev/uinput وينشئ الجهاز
//   (p *Pad) apply(d []byte) — يطبق حزمة 8 بايت (lx,ly,rx,ry | أزرار | LT | RT)
//
//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"syscall"
)

// ---------- uinput ----------
const (
	uinputDev = "/dev/uinput"

	evSyn, evKey, evAbs = 0, 1, 3

	uiSetEvBit  = 0x40045564
	uiSetKeyBit = 0x40045565
	uiSetAbsBit = 0x40045567
	uiDevCreate = 0x5501
)

// قناع XInput القادم من الهاتف -> زر لينكس (evdev keycode)
var buttons = []struct{ mask, code uint16 }{
	{0x1000, 0x130}, // A  (BTN_SOUTH)
	{0x2000, 0x131}, // B  (BTN_EAST)
	{0x4000, 0x133}, // X  (BTN_NORTH)
	{0x8000, 0x134}, // Y  (BTN_WEST)
	{0x0100, 0x136}, // LB (BTN_TL)
	{0x0200, 0x137}, // RB (BTN_TR)
	{0x0020, 0x13a}, // Back  (BTN_SELECT)
	{0x0010, 0x13b}, // Start (BTN_START)
	{0x0400, 0x13c}, // Home  (BTN_MODE)
	{0x0040, 0x13d}, // L3    (BTN_THUMBL)
	{0x0080, 0x13e}, // R3    (BTN_THUMBR)
}

const (
	absX, absY, absZ, absRX, absRY, absRZ = 0, 1, 2, 3, 4, 5
	absHat0X, absHat0Y                    = 0x10, 0x11
)

type userDev struct {
	Name                              [80]byte
	Bustype, Vendor, Product, Version uint16
	FFEffectsMax                      uint32
	Absmax, Absmin, Absfuzz, Absflat  [64]int32
}

type inputEvent struct {
	Sec, Usec  int64
	Type, Code uint16
	Value      int32
}

func ioctl(fd int, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg); e != 0 {
		return e
	}
	return nil
}

// Pad هو ذراع Xbox 360 افتراضي مدعوم عبر uinput على لينكس.
type Pad struct {
	padBase
	fd int
}

func newPad() (*Pad, error) {
	fd, err := syscall.Open(uinputDev, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("تعذر فتح %s (%w): قم بتشغيل 'make setup' أولاً لإعداد الصلاحيات", uinputDev, err)
	}
	fail := func(e error) (*Pad, error) { syscall.Close(fd); return nil, e }

	for _, t := range []uintptr{evKey, evAbs, evSyn} {
		if err := ioctl(fd, uiSetEvBit, t); err != nil {
			return fail(err)
		}
	}
	for _, b := range buttons {
		if err := ioctl(fd, uiSetKeyBit, uintptr(b.code)); err != nil {
			return fail(err)
		}
	}
	for _, a := range []uintptr{absX, absY, absZ, absRX, absRY, absRZ, absHat0X, absHat0Y} {
		if err := ioctl(fd, uiSetAbsBit, a); err != nil {
			return fail(err)
		}
	}

	var d userDev
	copy(d.Name[:], x360Name)
	d.Bustype, d.Vendor, d.Product, d.Version = 3, x360Vendor, x360Product, 0x110
	for _, a := range []int{absX, absY, absRX, absRY} {
		d.Absmin[a], d.Absmax[a], d.Absfuzz[a], d.Absflat[a] = -32768, 32767, 16, 128
	}
	for _, a := range []int{absZ, absRZ} {
		d.Absmin[a], d.Absmax[a] = 0, 255
	}
	for _, a := range []int{absHat0X, absHat0Y} {
		d.Absmin[a], d.Absmax[a] = -1, 1
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, &d)
	if _, err := syscall.Write(fd, buf.Bytes()); err != nil {
		return fail(err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		return fail(err)
	}
	return &Pad{fd: fd}, nil
}

func scale(v int8) int32 { return int32(v) * 32767 / 127 }

// 8 بايت: lx, ly, rx, ry (int8) | أزرار (uint16) | LT (uint8) | RT (uint8)
func (p *Pad) apply(d []byte) {
	if !p.begin(d) {
		return
	}
	defer p.end()
	if p.fd < 0 {
		return
	}

	btn := binary.LittleEndian.Uint16(d[4:6])
	b2i := func(c bool) int32 {
		if c {
			return 1
		}
		return 0
	}
	var buf bytes.Buffer
	ev := func(t, c uint16, v int32) {
		_ = binary.Write(&buf, binary.LittleEndian, inputEvent{Type: t, Code: c, Value: v})
	}
	ev(evAbs, absX, scale(int8(d[0])))
	ev(evAbs, absY, -scale(int8(d[1]))) // في لينكس الأعلى = سالب
	ev(evAbs, absRX, scale(int8(d[2])))
	ev(evAbs, absRY, -scale(int8(d[3])))
	ev(evAbs, absZ, int32(d[6]))                             // LT: عمق الضغط 0..255
	ev(evAbs, absRZ, int32(d[7]))                            // RT
	ev(evAbs, absHat0X, b2i(btn&0x8 != 0)-b2i(btn&0x4 != 0)) // يمين - يسار
	ev(evAbs, absHat0Y, b2i(btn&0x2 != 0)-b2i(btn&0x1 != 0)) // أسفل - أعلى
	for _, b := range buttons {
		ev(evKey, b.code, b2i(btn&b.mask != 0))
	}
	ev(evSyn, 0, 0)
	_, _ = syscall.Write(p.fd, buf.Bytes()) // خطأ الكتابة لا يغيّر شيئًا؛ الحزمة التالية تعيد المحاولة
}

// preflight: فحص مبكر عند التشغيل كي يظهر سبب أي فشل مرة واحدة بدل أن يتكرر مع كل اتصال.
func preflight() error {
	fd, err := syscall.Open(uinputDev, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("تعذر فتح %s (%w): قم بتشغيل 'make setup' أولاً لإعداد الصلاحيات، ثم سجّل الخروج والدخول", uinputDev, err)
	}
	syscall.Close(fd)
	return nil
}
