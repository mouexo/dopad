package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PropMeta describes how the editor should render a property.
type PropMeta struct {
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	Options []string `json:"options,omitempty"`
	Hint    string   `json:"hint,omitempty"`
}

type CatalogButton struct {
	Key   string `json:"key"`
	Value int    `json:"value"`
}

type Catalog struct {
	Source       string                    `json:"source"`
	MTime        int64                     `json:"mtime"`
	Buttons      []CatalogButton           `json:"buttons"`
	Icons        map[string]string         `json:"icons"`
	Types        []string                  `json:"types"`
	TypeProps    map[string][]string       `json:"type_props"`
	PartProps    []string                  `json:"part_props"`
	PropMeta     map[string]PropMeta       `json:"prop_meta"`
	CommonProps  []string                  `json:"common_props"`
	Defaults     map[string]map[string]any `json:"defaults"`
	PartDefaults map[string]map[string]any `json:"part_defaults"`
}

// ---------------------------------------------------------------------------
// JavaScript scanning helpers (mirrors the rules used by web/controls.js)
// ---------------------------------------------------------------------------

func skipString(src string, i int) int {
	if i >= len(src) {
		return i
	}
	quote := src[i]
	i++
	for i < len(src) {
		ch := src[i]
		if ch == '\\' {
			i += 2
			continue
		}
		if ch == quote {
			return i + 1
		}
		i++
	}
	return i
}

func skipLineComment(src string, i int) int {
	if end := strings.IndexByte(src[i:], '\n'); end >= 0 {
		return i + end + 1
	}
	return len(src)
}

func skipBlockComment(src string, i int) int {
	if end := strings.Index(src[i+2:], "*/"); end >= 0 {
		return i + 2 + end + 2
	}
	return len(src)
}

func depthAt(src string, pos int) int {
	depth := 0
	for i := 0; i < pos && i < len(src); {
		ch := src[i]
		if ch == '\'' || ch == '"' || ch == '`' {
			i = skipString(src, i)
			continue
		}
		if ch == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				i = skipLineComment(src, i)
				continue
			}
			if src[i+1] == '*' {
				i = skipBlockComment(src, i)
				continue
			}
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
		}
		i++
	}
	return depth
}

func matchingBrace(src string, start int) int {
	depth := 0
	for i := start; i < len(src); {
		ch := src[i]
		if ch == '\'' || ch == '"' || ch == '`' {
			i = skipString(src, i)
			continue
		}
		if ch == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				i = skipLineComment(src, i)
				continue
			}
			if src[i+1] == '*' {
				i = skipBlockComment(src, i)
				continue
			}
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

func objectBody(src, name string) string {
	re := regexp.MustCompile(`(?s)\bconst\s+` + regexp.QuoteMeta(name) + `\s*=\s*\{`)
	loc := re.FindStringIndex(src)
	if loc == nil {
		return ""
	}
	start := strings.IndexByte(src[loc[0]:loc[1]], '{') + loc[0]
	end := matchingBrace(src, start)
	if end < 0 {
		return ""
	}
	return src[start+1 : end]
}

func appendUnique(list []string, value string) []string {
	if value == "" {
		return list
	}
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func pm(kind, label string, options ...string) PropMeta {
	return PropMeta{Kind: kind, Label: label, Options: options}
}

func pmHint(kind, label, hint string, options ...string) PropMeta {
	return PropMeta{Kind: kind, Label: label, Options: options, Hint: hint}
}

// loadCatalog reads web/controls.js at runtime and extracts the button map,
// the icon set, the element builders, their properties, and editor metadata.
func loadCatalog(root string) (Catalog, error) {
	path := filepath.Join(root, "web", "controls.js")
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	src := string(data)

	catalog := Catalog{
		Source:       path,
		Buttons:      []CatalogButton{},
		Icons:        map[string]string{},
		Types:        []string{},
		TypeProps:    map[string][]string{},
		PartProps:    []string{},
		PropMeta:     map[string]PropMeta{},
		CommonProps:  []string{"anchor", "x", "y", "w", "h", "size", "z"},
		Defaults:     map[string]map[string]any{},
		PartDefaults: map[string]map[string]any{},
	}
	if info, err := os.Stat(path); err == nil {
		catalog.MTime = info.ModTime().UnixMilli()
	}

	// const BTN = { UP: 1, ... };
	btnBody := objectBody(src, "BTN")
	for _, match := range regexp.MustCompile(`([A-Za-z_]\w*)\s*:\s*(\d+)`).FindAllStringSubmatch(btnBody, -1) {
		var value int
		for _, ch := range match[2] {
			value = value*10 + int(ch-'0')
		}
		catalog.Buttons = append(catalog.Buttons, CatalogButton{Key: match[1], Value: value})
	}

	// const ICONS = { home: '<svg ...>', ... };
	iconBody := objectBody(src, "ICONS")
	iconRe := regexp.MustCompile(`(?s)([A-Za-z_]\w*)\s*:\s*'(([^'\\]|\\.)*)'`)
	for _, match := range iconRe.FindAllStringSubmatch(iconBody, -1) {
		catalog.Icons[match[1]] = strings.ReplaceAll(match[2], `\'`, `'`)
	}

	// const BUILD = { panel(o) { ... }, ... };
	buildBody := objectBody(src, "BUILD")
	fnRe := regexp.MustCompile(`([A-Za-z_]\w*)\s*\(\s*o\s*\)\s*\{`)
	propRe := regexp.MustCompile(`\bo\.([A-Za-z_]\w*)`)
	padBody := ""
	for _, match := range fnRe.FindAllStringSubmatchIndex(buildBody, -1) {
		start := match[0]
		if depthAt(buildBody, start) != 0 {
			continue
		}
		name := buildBody[match[2]:match[3]]
		brace := match[1] - 1
		end := matchingBrace(buildBody, brace)
		if end < 0 {
			continue
		}
		body := buildBody[brace+1 : end]
		catalog.Types = append(catalog.Types, name)
		if name == "pad" {
			padBody = body
		}

		props := []string{}
		for _, prop := range propRe.FindAllStringSubmatch(body, -1) {
			props = appendUnique(props, prop[1])
		}
		// trigDir(o) reads o.dir / o.from outside the builder body.
		if name == "trigger" {
			props = appendUnique(props, "dir")
			props = appendUnique(props, "from")
		}
		catalog.TypeProps[name] = props
	}

	// Pad parts use a second object, conventionally named p.
	partRe := regexp.MustCompile(`\bp\.([A-Za-z_]\w*)`)
	for _, prop := range partRe.FindAllStringSubmatch(padBody, -1) {
		catalog.PartProps = appendUnique(catalog.PartProps, prop[1])
	}
	if len(catalog.PartProps) == 0 {
		catalog.PartProps = []string{"type", "key", "angle", "arc", "poly", "spread", "sweep",
			"color", "label", "icon", "at", "size", "fs", "arrow", "latch", "dir"}
	}

	for name, props := range catalog.TypeProps {
		for _, common := range catalog.CommonProps {
			props = appendUnique(props, common)
		}
		catalog.TypeProps[name] = props
	}

	catalog.PropMeta = map[string]PropMeta{
		"anchor": pmHint("select", "الارتكاز", "tl/tr/bl/br زاوية، tc/bc منتصف الحافة، cc منتصف الشاشة", "tl", "tr", "bl", "br", "tc", "bc", "cc"),
		"x":      pmHint("number", "إزاحة أفقية", "بوحدة u (≈1% من ارتفاع الشاشة)"),
		"y":      pm("number", "إزاحة رأسية"),
		"w":      pmHint("dimension", "العرض", "رقم = u، أو نسبة مثل 50%"),
		"h":      pm("dimension", "الارتفاع"),
		"size":   pm("dimension", "الحجم (مربع)"),
		"z":      pm("number", "الطبقة"),
		"key":    pm("key", "الزر"),
		"label":  pm("text", "النص"),
		"icon":   pm("icon", "الأيقونة"),
		"shape":  pm("select", "الشكل", "pill", "round"),
		"color":  pm("select", "اللون", "a", "b", "x", "y"),
		"latch":  pm("number", "تثبيت (ms)"),
		"action": pm("select", "الإجراء", "menu"),
		"fill":   pm("bool", "تعبئة الخلفية"),
		"edge":   pm("select", "الحافة", "left", "right"),
		"side":   pm("select", "الجهة", "left", "right"),
		"area":   pm("select", "منطقة اللمس", "left", "right"),
		"ring":   pm("number", "قطر الحلقة"),
		"knob":   pm("number", "قطر المقبض"),
		"travel": pm("number", "أقصى إزاحة"),
		"reach":  pm("number", "مدى اللمس"),
		"rest":   pm("bool", "إظهار في السكون"),
		"dir":    pm("select", "اتجاه التعبئة", "cw", "ccw", "out", "in", "top", "bottom", "left", "right"),
		"from":   pm("select", "اتجاه التعبئة (بديل)", "top", "bottom", "left", "right"),
		"metric": pm("select", "المقياس", "circle", "diamond"),
		"spread": pm("number", "تسامح الزاوية"),
		"inner":  pm("number", "المنطقة الميتة"),
		"grow":   pm("number", "تسامح البعد"),
		"parts":  pm("parts", "الأجزاء"),
		"type":   pm("select", "النوع", "button", "trigger"),
		"angle":  pm("number", "الزاوية"),
		"arc":    pm("quad", "قوس [r1, r2, a0, a1]"),
		"poly":   pm("poly", "مضلّع (x,y x,y …)"),
		"sweep":  pm("pair", "المدى الزاوي [a0, a1]"),
		"at":     pm("pair", "موضع النص [x, y]"),
		"fs":     pm("number", "حجم الخط"),
		"arrow":  pm("bool", "سهم الاتجاه"),
	}

	// Defaults used when a new element is added.
	padParts := []any{
		map[string]any{"key": "UP", "angle": 0, "arc": []any{24, 48, -40, 40}, "arrow": true},
		map[string]any{"key": "RIGHT", "angle": 90, "arc": []any{24, 48, 50, 130}, "arrow": true},
		map[string]any{"key": "DOWN", "angle": 180, "arc": []any{24, 48, 140, 220}, "arrow": true},
		map[string]any{"key": "LEFT", "angle": 270, "arc": []any{24, 48, 230, 310}, "arrow": true},
	}
	catalog.Defaults = map[string]map[string]any{
		"panel": {
			"type": "panel", "anchor": "tl", "x": 0, "y": 0,
			"w": "50%", "h": "100%", "fill": true, "edge": "right", "label": "لوحة",
		},
		"button": {
			"type": "button", "key": "A", "label": "A", "shape": "round",
			"w": 13, "anchor": "cc", "x": 0, "y": 0,
		},
		"trigger": {
			"type": "trigger", "key": "LT", "label": "LT",
			"anchor": "tl", "x": 3, "y": 14, "w": 18, "h": 34,
		},
		"stick": {
			"type": "stick", "side": "left", "area": "left", "ring": 36, "knob": 15.5,
		},
		"pad": {
			"type": "pad", "anchor": "bl", "x": 5, "y": 4, "size": 38,
			"spread": 60, "inner": 0.24, "reach": 1.08, "parts": padParts,
		},
	}
	catalog.PartDefaults = map[string]map[string]any{
		"button": {
			"type": "button", "key": "A", "angle": 0,
			"arc": []any{24, 48, -40, 40}, "label": "A", "at": []any{50, 20},
		},
		"trigger": {
			"type": "trigger", "key": "LT", "angle": 0,
			"arc": []any{30, 46, -30, 30},
		},
	}

	return catalog, nil
}
