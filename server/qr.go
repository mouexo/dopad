// qr.go — مولد رموز QR كود من الصفر (Go stdlib فقط، لا dependencies).
//
// يدعم إصدارات 1–5 مع مستويات L و M لوضعية البايت (byte mode).
// BCH لترميز معلومات التنسيق، Reed-Solomon GF(256) للتصحيح،
// و 8 نماذج قناع (mask).
//
// يُستخدم لعرض URL الخادم على الهاتف — يُطبع كـ ASCII في الطرفية ويُخدم
// كـ PNG عبر مسار /qr.

package main

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
)

// ---------------------------------------------------------------------------
// GF(256) — جدول اللوغاريتهم العكسي للضرب في المجموعة المنتهية الحقلية.
// الكمية الأصلية: x^8 + x^4 + x^3 + x^2 + 1 = 0x11D
// ---------------------------------------------------------------------------

var gfExp [512]int
var gfLog [256]int

// جدولا GF يُبنيان مرة واحدة عند بدء البرنامج. بناؤهما داخل GenerateQR كان
// يكتب في متغيّرين عامّين من كل طلب /qr، وهو سباق بيانات عند تعدد الطلبات.
func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
			x &= 0xFF
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[(gfLog[a]+gfLog[b])%255]
}

// ---------------------------------------------------------------------------
// Reed-Solomon
// ---------------------------------------------------------------------------

// rsGenPoly يبني متعدد الحدودات المولدي: g(x) = Π(x + α^i), i = 0..degree-1
// يُرجع النوافذ من أعلى الحد إلى الثابت (gen[0]=leading=1).
func rsGenPoly(degree int) []int {
	p := []int{1}
	for i := 0; i < degree; i++ {
		np := make([]int, len(p)+1)
		for j := 0; j < len(p); j++ {
			np[j] ^= gfMul(p[j], gfExp[i])
			np[j+1] ^= p[j]
		}
		p = np
	}
	// عكس: p يُنتج [ثابت، ..., أعلى حد] لكن rsEncode يتوقع [أعلى حد، ..., ثابت]
	result := make([]int, len(p))
	for i := range p {
		result[i] = p[len(p)-1-i]
	}
	return result
}

// rsEncode يحسب EC bytes لـ data.
func rsEncode(data []byte, ecBytes int) []byte {
	if ecBytes == 0 {
		return nil
	}
	gen := rsGenPoly(ecBytes)
	buf := make([]int, len(data)+ecBytes)
	for i, b := range data {
		buf[i] = int(b)
	}
	for i := 0; i < len(data); i++ {
		coef := buf[i]
		if coef != 0 {
			for j := 0; j < len(gen); j++ {
				buf[i+j] ^= gfMul(gen[j], coef)
			}
		}
	}
	ec := make([]byte, ecBytes)
	for i := 0; i < ecBytes; i++ {
		ec[i] = byte(buf[len(data)+i])
	}
	return ec
}

// ---------------------------------------------------------------------------
// جداول إصدارات QR
// ---------------------------------------------------------------------------

// مستويا تصحيح الأخطاء المدعومان (level << 3 | mask في معلومات التنسيق).
const (
	ecLevelM = 0 // 00
	ecLevelL = 1 // 01
)

type rsBlock struct {
	totalCount int
	dataCount  int
}

type qrVersion struct {
	version   int
	size      int
	dataBytes int
	ecBytes   int
	blocks    []rsBlock
}

// إصدارات 1–5 مع مستويات L و M (كافٍ لعناوين URL المحليّة).
var qrVersionL = []qrVersion{
	{1, 21, 19, 7, []rsBlock{{26, 19}}},
	{2, 25, 34, 10, []rsBlock{{44, 34}}},
	{3, 29, 55, 15, []rsBlock{{70, 55}}},
	{4, 33, 80, 20, []rsBlock{{100, 80}}},
	{5, 37, 108, 26, []rsBlock{{134, 108}}},
}

var qrVersionM = []qrVersion{
	{1, 21, 16, 10, []rsBlock{{26, 16}}},
	{2, 25, 28, 16, []rsBlock{{44, 28}}},
	{3, 29, 44, 26, []rsBlock{{70, 44}}},
	{4, 33, 64, 18, []rsBlock{{50, 32}, {50, 32}}},
	{5, 37, 86, 24, []rsBlock{{67, 43}, {67, 43}}},
}

// pickVersion يختار أصغر إصدار يتناسب مع data بمستوى ecLevel.
func pickVersion(dataLen int, ecLevel int) qrVersion {
	neededBits := 12 + 8*dataLen + 4 // mode(4) + cc(8) + data + terminator(4)
	tbl := qrVersionL
	if ecLevel == ecLevelM {
		tbl = qrVersionM
	}
	for _, v := range tbl {
		if v.dataBytes*8 >= neededBits {
			return v
		}
	}
	// fallback to largest
	return tbl[len(tbl)-1]
}

// ---------------------------------------------------------------------------
// BCH — معلومات التنسيق
// ---------------------------------------------------------------------------

const (
	qrG15     = 0x537 // x^10+x^8+x^5+x^4+x^2+x+1 (ISO/IEC 18004 format info generator)
	qrG15Mask = 0x5412
)

func bchDigit(d int) int {
	n := 0
	for d != 0 {
		n++
		d >>= 1
	}
	return n
}

func bchTypeInfo(level, mask int) int {
	data := (level << 3) | mask
	d := data << 10
	for bchDigit(d)-bchDigit(qrG15) >= 0 {
		d ^= qrG15 << (bchDigit(d) - bchDigit(qrG15))
	}
	return ((data << 10) | d) ^ qrG15Mask
}

// ---------------------------------------------------------------------------
// نمط القناءق (mask) — 8 قواعد حسب المواصفة
// ---------------------------------------------------------------------------

func maskFunc(pattern, row, col int) bool {
	switch pattern {
	case 0:
		return (row+col)%2 == 0
	case 1:
		return row%2 == 0
	case 2:
		return col%3 == 0
	case 3:
		return (row+col)%3 == 0
	case 4:
		return ((row/2)+(col/3))%2 == 0
	case 5:
		ij := row * col
		return (ij%2 + ij%3) == 0
	case 6:
		ij := row * col
		return ((ij%2 + ij%3) % 2) == 0
	case 7:
		ij := row * col
		return ((ij%3 + (row+col)%2) % 2) == 0
	}
	return false
}

// ---------------------------------------------------------------------------
// أنماط التوافقية (function patterns)
// ---------------------------------------------------------------------------

// مراكز نمط التوافق (alignment patterns) حسب الإصدار.
var alignmentPositions = [][]int{
	{},      // v1
	{6, 18}, // v2
	{6, 22}, // v3
	{6, 26}, // v4
	{6, 30}, // v5
}

// qrMatrix — مصفوفة QR الكاملة.
type qrMatrix struct {
	size int
	dark [][]bool
	set  [][]bool // true إذا تم تعيينها بواسطة أنماط توافقية
}

func newMatrix(size int) *qrMatrix {
	m := &qrMatrix{
		size: size,
		dark: make([][]bool, size),
		set:  make([][]bool, size),
	}
	for i := 0; i < size; i++ {
		m.dark[i] = make([]bool, size)
		m.set[i] = make([]bool, size)
	}
	return m
}

func (m *qrMatrix) setupFinder(row, col int) {
	for r := -1; r < 8; r++ {
		for c := -1; c < 8; c++ {
			rr, cc := row+r, col+c
			if rr < 0 || rr >= m.size || cc < 0 || cc >= m.size {
				continue
			}
			isDark := (0 <= r && r <= 6 && (c == 0 || c == 6)) ||
				(0 <= c && c <= 6 && (r == 0 || r == 6)) ||
				(2 <= r && r <= 4 && 2 <= c && c <= 4)
			m.dark[rr][cc] = isDark
			m.set[rr][cc] = true
		}
	}
}

func (m *qrMatrix) setupTiming() {
	for r := 8; r < m.size-8; r++ {
		m.dark[r][6] = r%2 == 0
		m.set[r][6] = true
	}
	for c := 8; c < m.size-8; c++ {
		m.dark[6][c] = c%2 == 0
		m.set[6][c] = true
	}
}

func (m *qrMatrix) setupAlignment(ver int) {
	pos := alignmentPositions[ver-1]
	for _, r := range pos {
		for _, c := range pos {
			if m.set[r][c] {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					rr, cc := r+dr, c+dc
					if rr < 0 || rr >= m.size || cc < 0 || cc >= m.size {
						continue
					}
					isDark := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
					m.dark[rr][cc] = isDark
					m.set[rr][cc] = true
				}
			}
		}
	}
}

// setupFormat يكتب بتات معلومات التنسيق (15 بت) في موضعيها، ثم يضبط
// الوحدة المظلمة. allLight يستعمله وضع الاختبار (بتات فاتحة) لحساب العقوبة.
func (m *qrMatrix) setupFormat(bits int, allLight bool) {
	for i := 0; i < 15; i++ {
		bit := !allLight && (bits>>i)&1 == 1

		var row int
		switch {
		case i < 6:
			row = i
		case i < 8:
			row = i + 1
		default:
			row = m.size - 15 + i
		}
		m.dark[row][8] = bit
		m.set[row][8] = true

		var col int
		switch {
		case i < 8:
			col = m.size - i - 1
		case i < 9:
			col = 7
		default:
			col = 15 - i - 1
		}
		m.dark[8][col] = bit
		m.set[8][col] = true
	}

	// الوحدة المظلمة (dark module) — دائماً مظلمة
	m.dark[m.size-8][8] = true
	m.set[m.size-8][8] = true
}

func (m *qrMatrix) setupTypeInfo(level, mask int) {
	m.setupFormat(bchTypeInfo(level, mask), false)
}

// setupTypeInfoTest يضبط بتات معلومات النوع إلى الفاتح — لحساب العقوبة فقط.
func (m *qrMatrix) setupTypeInfoTest() {
	m.setupFormat(0, true)
}

// ---------------------------------------------------------------------------
// ترميز البيانات (byte mode)
// ---------------------------------------------------------------------------

// bitWriter يكتب البتات MSB-first مباشرة في بايتات، بدل بناء شريحة بتات
// منفصلة ثم تحويلها. يوفّر نصف الذاكرة ويبسّط createCodewords.
type bitWriter struct {
	data []byte
	n    int // عدد البتات المكتوبة
}

func (w *bitWriter) bit(b byte) {
	if w.n%8 == 0 {
		w.data = append(w.data, 0)
	}
	w.data[w.n/8] |= (b & 1) << uint(7-w.n%8)
	w.n++
}

func (w *bitWriter) bits(v uint32, count int) {
	for i := count - 1; i >= 0; i-- {
		w.bit(byte(v>>i) & 1)
	}
}

// createCodewords يرمّز البيانات (byte mode) ويضيف الإنهاء والحشو.
func createCodewords(data []byte, v qrVersion) []byte {
	w := &bitWriter{data: make([]byte, 0, v.dataBytes)}
	w.bits(4, 4)                 // وضع البايت: 0100
	w.bits(uint32(len(data)), 8) // عدد الأحرف (8 بت للنسخ 1-9)
	for _, b := range data {
		w.bits(uint32(b), 8)
	}

	// إنهاء (terminator) بما يصل 4 أصفار، ثم محاذاة إلى حدود بايت.
	remaining := v.dataBytes*8 - w.n
	if remaining > 4 {
		remaining = 4
	}
	w.bits(0, remaining)
	if pad := w.n % 8; pad != 0 {
		w.bits(0, 8-pad)
	}

	// حشو بالبايتين 0xEC و0x11 بالتناوب.
	for i := 0; w.n/8 < v.dataBytes; i++ {
		if i%2 == 0 {
			w.bits(0xEC, 8)
		} else {
			w.bits(0x11, 8)
		}
	}
	return w.data
}

// rsInterleave ترصيف بيانات EC للكتل متعددة.
func rsInterleave(data []byte, v qrVersion) []byte {
	if len(v.blocks) == 1 {
		// كتلة واحدة: data || ec
		ec := rsEncode(data, v.ecBytes)
		return append(data, ec...)
	}

	// كتل متعددة: ترصيف البيانات والـ EC
	ecPerBlock := make([]int, len(v.blocks))
	dataStart := 0
	for i, b := range v.blocks {
		ecPerBlock[i] = b.totalCount - b.dataCount
		dataStart += b.dataCount
	}

	// فرز الكتل: كل كتلة data + ec
	allData := make([][]byte, len(v.blocks))
	allEC := make([][]byte, len(v.blocks))
	offset := 0
	for i, b := range v.blocks {
		allData[i] = data[offset : offset+b.dataCount]
		offset += b.dataCount
		allEC[i] = rsEncode(allData[i], ecPerBlock[i])
	}

	// ترصيف: data[0][0], data[1][0], ..., ec[0][0], ec[1][0], ...
	var result []byte
	maxData := 0
	for _, b := range v.blocks {
		if b.dataCount > maxData {
			maxData = b.dataCount
		}
	}
	for i := 0; i < maxData; i++ {
		for _, db := range allData {
			if i < len(db) {
				result = append(result, db[i])
			}
		}
	}
	maxEC := 0
	for _, ec := range ecPerBlock {
		if ec > maxEC {
			maxEC = ec
		}
	}
	for i := 0; i < maxEC; i++ {
		for _, eb := range allEC {
			if i < len(eb) {
				result = append(result, eb[i])
			}
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// وضع البيانات في المصفوفة (zigzag)
// ---------------------------------------------------------------------------

func (m *qrMatrix) placeData(codewords []byte, mask int) {
	inc := -1 // اتجاه: للأعلى
	row := m.size - 1
	bitIdx := 7 // MSB أولاً داخل كل بايت
	byteIdx := 0
	dataLen := len(codewords)

	for col := m.size - 1; col > 0; col -= 2 {
		c := col
		if c <= 6 {
			c -= 1 // تخطي عمود التوقيت 6
		}
		for {
			for _, cc := range [2]int{c, c - 1} {
				if cc < 0 || cc >= m.size {
					continue
				}
				if m.set[row][cc] {
					continue // مشغول بأنماط توافقية
				}
				dark := byte(0)
				if byteIdx < dataLen {
					dark = (codewords[byteIdx] >> bitIdx) & 1
				}
				if maskFunc(mask, row, cc) {
					dark ^= 1
				}
				m.dark[row][cc] = dark == 1
				m.set[row][cc] = true
				// Decrement bit index per module (matches Python's map_data)
				bitIdx--
				if bitIdx < 0 {
					bitIdx = 7
					byteIdx++
				}
			}
			row += inc
			if row < 0 || row >= m.size {
				row -= inc
				inc = -inc
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// حساب العقابة (penalty) لاختيار أفضل قناع
// ---------------------------------------------------------------------------

func (m *qrMatrix) penalty() int {
	n := m.size
	penalty := 0

	// المستوى 1: تتالي بكساوات نفس اللون (≥5)
	for i := 0; i < n; i++ {
		// صف
		run := 1
		for j := 1; j < n; j++ {
			if m.dark[i][j] == m.dark[i][j-1] {
				run++
			} else {
				if run >= 5 {
					penalty += run - 2
				}
				run = 1
			}
		}
		if run >= 5 {
			penalty += run - 2
		}

		// عمود
		run = 1
		for j := 1; j < n; j++ {
			if m.dark[j][i] == m.dark[j-1][i] {
				run++
			} else {
				if run >= 5 {
					penalty += run - 2
				}
				run = 1
			}
		}
		if run >= 5 {
			penalty += run - 2
		}
	}

	// المستوى 2: مربعات 2×2 نفس اللون
	for r := 0; r < n-1; r++ {
		for c := 0; c < n-1; c++ {
			if m.dark[r][c] == m.dark[r][c+1] &&
				m.dark[r][c] == m.dark[r+1][c] &&
				m.dark[r][c] == m.dark[r+1][c+1] {
				penalty += 3
			}
		}
	}

	// المستوى 3: نمط 1011101 مع مساحة فارغة 4 على الجانبين
	pat := [7]bool{true, false, true, true, true, false, true}
	// أفقي
	for r := 0; r < n; r++ {
		for c := 0; c <= n-7; c++ {
			match := true
			for k := 0; k < 7; k++ {
				if m.dark[r][c+k] != pat[k] {
					match = false
					break
				}
			}
			if match {
				// تحقق من المساحة المحيطة
				leftOK := c >= 4
				for k := 0; k < 4; k++ {
					if leftOK && m.dark[r][c-k-1] {
						leftOK = false
					}
				}
				rightOK := c+7+3 < n
				for k := 0; k < 4; k++ {
					if rightOK && m.dark[r][c+7+k] {
						rightOK = false
					}
				}
				if leftOK || rightOK {
					penalty += 40
				}
			}
		}
	}
	// عمودي
	for c := 0; c < n; c++ {
		for r := 0; r <= n-7; r++ {
			match := true
			for k := 0; k < 7; k++ {
				if m.dark[r+k][c] != pat[k] {
					match = false
					break
				}
			}
			if match {
				leftOK := r >= 4
				for k := 0; k < 4; k++ {
					if leftOK && m.dark[r-k-1][c] {
						leftOK = false
					}
				}
				rightOK := r+7+3 < n
				for k := 0; k < 4; k++ {
					if rightOK && m.dark[r+7+k][c] {
						rightOK = false
					}
				}
				if leftOK || rightOK {
					penalty += 40
				}
			}
		}
	}

	// المستوى 4: نسبة المظلم إلى الكلي
	darkCount := 0
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if m.dark[r][c] {
				darkCount++
			}
		}
	}
	ratio := darkCount * 100 / (n * n)
	penalty += (abs(ratio-50) / 5) * 10

	return penalty
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---------------------------------------------------------------------------
// بناء QR الكامل
// ---------------------------------------------------------------------------

// QRCode يمثّل رمز QR الكامل بعد التوليد.
type QRCode struct {
	matrix *qrMatrix
}

// GenerateQR يُنشئ رمز QR للبيانات المعطاة.
func GenerateQR(data string) *QRCode {
	ecLevel := ecLevelL
	v := pickVersion(len(data), ecLevel)

	// ترميز البيانات والـ EC
	codewords := createCodewords([]byte(data), v)
	fullCodewords := rsInterleave(codewords, v)

	// بناء المصفوفة واختيار أفضل قناع
	// نستخدم وضع الاختبار (test=True) — بدون تطبيق بتات معلومات النوع —
	// لحساب العقابة، تطابقًا مع مكتبة Python qrcode.
	bestMask := 0
	bestPenalty := -1
	var bestMatrix *qrMatrix

	for mask := 0; mask < 8; mask++ {
		m := newMatrix(v.size)
		m.setupFinder(0, 0)
		m.setupFinder(0, m.size-7)
		m.setupFinder(m.size-7, 0)
		m.setupTiming()
		m.setupAlignment(v.version)
		m.setupTypeInfoTest() // test mode: format info bits all light
		m.placeData(fullCodewords, mask)

		p := m.penalty()
		if bestPenalty < 0 || p < bestPenalty {
			bestPenalty = p
			bestMask = mask
			bestMatrix = m
		}
	}

	// Apply actual type info (with correct G15=0x537) on the best matrix
	bestMatrix.setupTypeInfo(ecLevel, bestMask)
	return &QRCode{matrix: bestMatrix}
}

// ---------------------------------------------------------------------------
// العرض (Rendering)
// ---------------------------------------------------------------------------

// ToASCII يُعيد الرمز كنص باستخدام شخصيات نصف المربع.
// يطبع صفين في سطر واحد لتوفير المساحة على الطرفية.
func (q *QRCode) ToASCII() string {
	m := q.matrix
	var buf bytes.Buffer
	// حافة فاتحة
	for r := 0; r < m.size; r += 2 {
		for c := 0; c < m.size; c++ {
			top := r < m.size && m.dark[r][c]
			bottom := r+1 < m.size && m.dark[r+1][c]
			switch {
			case top && bottom:
				buf.WriteRune('\u2588') // █
			case top:
				buf.WriteRune('\u2580') // ▀
			case bottom:
				buf.WriteRune('\u2584') // ▄
			default:
				buf.WriteByte(' ')
			}
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}

// ToPNG ينتج صورة PNG للرمز مع حدود صامتة (quiet zone = 4 modules).
func (q *QRCode) ToPNG(scale int) []byte {
	m := q.matrix
	const border = 4
	width := (m.size + border*2) * scale
	img := image.NewRGBA(image.Rect(0, 0, width, width))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	// ارسم الوحدات الداكنة كمربّعات صلبة.
	for r := 0; r < m.size; r++ {
		for c := 0; c < m.size; c++ {
			if m.dark[r][c] {
				x, y := (c+border)*scale, (r+border)*scale
				draw.Draw(img, image.Rect(x, y, x+scale, y+scale), image.Black, image.Point{}, draw.Src)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
