package main

import (
	"bytes"
	"sync"
)

// هوية الذراع الافتراضية: نفس الاسم والمعرّفات على لينكس وويندوز كي تتعرف
// عليه الألعاب كذراع Xbox 360 حقيقية.
const (
	x360Name    = "Microsoft X-Box 360 pad"
	x360Vendor  = 0x045e // Microsoft
	x360Product = 0x028e // Xbox 360 Controller
)

// padBase يحمل الحالة المشتركة بين ذراع لينكس (uinput) وذراع ويندوز (ViGEm):
// قفل واحد وآخر حزمة مُطبَّقة (لمنع إعادة إرسال نفس الحالة).
type padBase struct {
	mu   sync.Mutex
	last []byte
}

// begin يقفل الذراع ويقبل حزمة من 8 بايت إن كانت مختلفة عن السابقة.
// عند النجاح يجب على المستدعي استدعاء end بعد تطبيق الأحداث.
func (b *padBase) begin(d []byte) bool {
	b.mu.Lock()
	if len(d) != 8 || bytes.Equal(d, b.last) {
		b.mu.Unlock()
		return false
	}
	b.last = append(b.last[:0], d...)
	return true
}

func (b *padBase) end() { b.mu.Unlock() }
