package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type LayoutInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	MTime int64  `json:"mtime"`
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateID(id string) error {
	if id == "" || len(id) > 80 || id == "." || id == ".." || !idRe.MatchString(id) {
		return errors.New("معرّف تخطيط غير صالح (استخدم حروفًا إنجليزية وأرقامًا و . _ - فقط)")
	}
	return nil
}

func layoutDir(root string) string {
	return filepath.Join(root, "layouts")
}

func listLayouts(root string) ([]LayoutInfo, error) {
	dir := layoutDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []LayoutInfo{}, nil
		}
		return nil, err
	}

	list := make([]LayoutInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		name := id
		path := filepath.Join(dir, entry.Name())
		var doc struct {
			Name string `json:"name"`
		}
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &doc) == nil && strings.TrimSpace(doc.Name) != "" {
			name = doc.Name
		}
		info := LayoutInfo{ID: id, Name: name}
		if st, err := entry.Info(); err == nil {
			info.MTime = st.ModTime().UnixMilli()
		}
		list = append(list, info)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

func loadLayout(root, id string) ([]byte, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	path := filepath.Join(layoutDir(root), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("التخطيط غير موجود: " + id)
		}
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("ملف التخطيط ليس JSON صالحًا")
	}
	return data, nil
}

func saveLayout(root, id string, body []byte) (string, error) {
	if err := validateID(id); err != nil {
		return "", err
	}
	if len(body) > 4<<20 {
		return "", errors.New("حجم التخطيط كبير جدًا")
	}
	if !json.Valid(body) {
		return "", errors.New("JSON غير صالح")
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", errors.New("JSON غير صالح: " + err.Error())
	}
	if raw, ok := doc["elements"]; ok {
		if _, ok := raw.([]any); !ok {
			return "", errors.New("الحقل elements يجب أن يكون مصفوفة")
		}
	} else {
		return "", errors.New("التخطيط يجب أن يحتوي على الحقل elements")
	}
	if raw, ok := doc["name"]; ok {
		if _, ok := raw.(string); !ok {
			return "", errors.New("الحقل name يجب أن يكون نصًا")
		}
	}

	path := filepath.Join(layoutDir(root), id+".json")
	if err := atomicWrite(path, body); err != nil {
		return "", err
	}
	return path, nil
}
