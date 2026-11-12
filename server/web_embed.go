//go:build webembed

package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed web
var embeddedWeb embed.FS

// عند البناء بالوسم webembed (لأن server/web موجود) نستخرج الواجهة إلى
// مجلد مؤقت فريد لكل تشغيل ونوجّه findWeb إليه. مجلد فريد ضروري كي لا
// تحذف نسخةٌ تعمل مجلد نسخة أخرى؛ أي web خارجي بجانب الثنائي يبقى له الأولوية.
func init() {
	if findWeb() != "" {
		return
	}
	dir, err := os.MkdirTemp("", "dopad-web-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "تعذّر إنشاء مجلد مؤقت للواجهة المضمّنة:", err)
		return
	}
	if err := os.CopyFS(dir, embeddedWeb); err != nil {
		fmt.Fprintln(os.Stderr, "تعذّر استخراج الواجهة المضمّنة:", err)
		return
	}
	_ = os.Setenv("DOPAD_WEB", filepath.Join(dir, "web"))
	if os.Getenv("DOPAD_LAYOUTS") == "" {
		if d := externalLayoutsDir(); d != "" {
			_ = os.Setenv("DOPAD_LAYOUTS", d)
		}
	}
}

// externalLayoutsDir يبحث عن layouts خارجي بجانب الثنائي أو في مجلد التشغيل،
// كي تبقى التخطيطات قابلة للتعديل حتى مع واجهة مضمّنة.
func externalLayoutsDir() string {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(d, "layouts"), filepath.Join(d, "..", "layouts"))
	}
	candidates = append(candidates, "layouts", filepath.Join("..", "layouts"))
	for _, d := range candidates {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}
