package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// findRoot locates the DOpad project root (the directory that holds
// web/controls.js). It checks an explicit flag, the environment, the
// executable location, and finally the working directory.
func findRoot(explicit string) string {
	var candidates []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		candidates = append(candidates, dir)
	}
	add(explicit)
	add(os.Getenv("DOPAD_ROOT"))

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 10; i++ {
			add(dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for i := 0; i < 10; i++ {
			add(dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	for _, dir := range candidates {
		if st, err := os.Stat(filepath.Join(dir, "web", "controls.js")); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(dir); err == nil {
				return abs
			}
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func isExecutable(info os.FileInfo) bool {
	if info == nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// detectBinary returns the first usable DOpad executable it can find.
func detectBinary(root string) string {
	var candidates []string
	if v := strings.TrimSpace(os.Getenv("DOPAD_BIN")); v != "" {
		candidates = append(candidates, v)
	}
	candidates = append(candidates,
		filepath.Join(root, "DOpad"),
		filepath.Join(root, "DOpad.exe"),
		filepath.Join(root, "server", "DOpad"),
	)

	dist := filepath.Join(root, "dist")
	if entries, err := os.ReadDir(dist); err == nil {
		type found struct {
			path string
			mod  int64
			web  bool
		}
		var list []found
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			for _, name := range []string{"DOpad", "DOpad.exe"} {
				path := filepath.Join(dist, entry.Name(), name)
				if info, err := os.Stat(path); err == nil && isExecutable(info) {
					list = append(list, found{
						path: path,
						mod:  info.ModTime().UnixNano(),
						web:  strings.Contains(entry.Name(), "+web"),
					})
				}
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].web != list[j].web {
				return !list[i].web
			}
			return list[i].mod > list[j].mod
		})
		for _, item := range list {
			candidates = append(candidates, item.path)
		}
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !isExecutable(info) {
			continue
		}
		if abs, err := filepath.Abs(candidate); err == nil {
			return abs
		}
		return candidate
	}
	return ""
}

func resolveBinary(root, given string) (string, error) {
	target := strings.TrimSpace(given)
	if target == "" {
		target = detectBinary(root)
	}
	if target == "" {
		return "", errors.New("لم أجد ملف DOpad التنفيذي. ابنِه بالأمر make build أو حدّد مساره يدويًا.")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		abs = target
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", errors.New("ملف الخادم غير موجود: " + abs)
	}
	if !isExecutable(info) {
		return "", errors.New("الملف ليس قابلًا للتنفيذ: " + abs)
	}
	return abs, nil
}

func hasExecutable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".dopad-panel-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)

	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(path, data)
}
