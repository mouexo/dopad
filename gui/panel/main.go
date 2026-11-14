package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFiles embed.FS

var version = "dev"

type Settings struct {
	Mode   string `json:"mode"`
	Port   string `json:"port"`
	Binary string `json:"binary"`
}

type App struct {
	root     string
	mgr      *Manager
	token    string
	version  string
	mu       sync.Mutex
	settings Settings
}

func defaultSettings() Settings {
	return Settings{Mode: "wifi", Port: "8080", Binary: ""}
}

// settingsPath keeps the panel settings inside layouts/panel. The extra folder
// matters: DOpad lists every *.json directly inside layouts/ as a playable
// layout, and directories are skipped.
func settingsPath(root string) string {
	return filepath.Join(root, "layouts", "panel", "settings.json")
}

func loadSettings(root string) Settings {
	settings := defaultSettings()
	_ = readJSON(settingsPath(root), &settings)
	switch settings.Mode {
	case "wifi", "bt", "self":
	default:
		settings.Mode = "wifi"
	}
	return settings
}

func saveSettings(root string, settings Settings) error {
	return writeJSONFile(settingsPath(root), settings)
}

func randomToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}

func openBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	_ = cmd.Start()
}

func (a *App) getSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *App) setSettings(settings Settings) {
	a.mu.Lock()
	a.settings = settings
	a.mu.Unlock()
	_ = saveSettings(a.root, settings)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(parsed.Host)
}

func (a *App) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		token := r.Header.Get("X-DOpad-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (a *App) serveIndex(w http.ResponseWriter) {
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "index not found", http.StatusInternalServerError)
		return
	}
	inject := `<script>window.DOPAD_TOKEN=` + strconv.Quote(a.token) + `;</script>`
	html := strings.Replace(string(data), "<!--DOPAD_TOKEN-->", inject, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, html)
}

func (a *App) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean(r.URL.Path)
	if clean == "/" || clean == "." || clean == "/index.html" {
		a.serveIndex(w)
		return
	}
	name := "web" + clean
	if !strings.HasPrefix(name, "web/") {
		http.NotFound(w, r)
		return
	}
	data, err := webFiles.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(clean))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasSuffix(clean, ".js") {
		contentType = "text/javascript; charset=utf-8"
	}
	if strings.HasSuffix(clean, ".css") {
		contentType = "text/css; charset=utf-8"
	}
	if strings.HasSuffix(clean, ".svg") {
		contentType = "image/svg+xml"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.serveStatic)
	mux.HandleFunc("/api/state", a.guard(a.apiState))
	mux.HandleFunc("/api/catalog", a.guard(a.apiCatalog))
	mux.HandleFunc("/api/qr", a.guard(a.apiQR))
	mux.HandleFunc("/api/logs", a.guard(a.apiLogs))
	mux.HandleFunc("/api/clear-logs", a.guard(a.apiClearLogs))
	mux.HandleFunc("/api/start", a.guard(a.apiStart))
	mux.HandleFunc("/api/stop", a.guard(a.apiStop))
	mux.HandleFunc("/api/settings", a.guard(a.apiSettings))
	mux.HandleFunc("/api/layouts", a.guard(a.apiLayouts))
	mux.HandleFunc("/api/layouts/", a.guard(a.apiLayout))
	mux.HandleFunc("/api/quit", a.guard(a.apiQuit))
	return a.securityHeaders(mux)
}

func (a *App) apiState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.mgr.State(a.getSettings(), a.version))
}

func (a *App) apiCatalog(w http.ResponseWriter, _ *http.Request) {
	catalog, err := loadCatalog(a.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("تعذّرت قراءة web/controls.js: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (a *App) apiQR(w http.ResponseWriter, _ *http.Request) {
	data, err := a.mgr.FetchQR()
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (a *App) apiLogs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	seq, lines := a.mgr.Logs(after)
	writeJSON(w, http.StatusOK, map[string]any{"seq": seq, "lines": lines})
}

func (a *App) apiClearLogs(w http.ResponseWriter, _ *http.Request) {
	seq := a.mgr.ClearLogs()
	writeJSON(w, http.StatusOK, map[string]any{"seq": seq})
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) error {
	if r.Method != http.MethodPost {
		return errors.New("method not allowed")
	}
	body := http.MaxBytesReader(w, r.Body, 256<<10)
	decoder := json.NewDecoder(body)
	return decoder.Decode(out)
}

func (a *App) apiStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode     string `json:"mode"`
		Port     string `json:"port"`
		Binary   string `json:"binary"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("طلب غير صالح: %w", err))
		return
	}
	settings := a.getSettings()
	if req.Mode == "" {
		req.Mode = settings.Mode
	}
	if strings.TrimSpace(req.Port) == "" {
		req.Port = "8080"
	}
	if err := a.mgr.Start(req.Mode, req.Port, req.Binary, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings = Settings{Mode: req.Mode, Port: req.Port, Binary: req.Binary}
	a.setSettings(settings)
	writeJSON(w, http.StatusOK, a.mgr.State(settings, a.version))
}

func (a *App) apiStop(w http.ResponseWriter, _ *http.Request) {
	if err := a.mgr.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, a.mgr.State(a.getSettings(), a.version))
}

func (a *App) apiSettings(w http.ResponseWriter, r *http.Request) {
	var req Settings
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("طلب غير صالح: %w", err))
		return
	}
	switch req.Mode {
	case "wifi", "bt", "self":
	default:
		writeError(w, http.StatusBadRequest, errors.New("وضع تشغيل غير معروف"))
		return
	}
	if req.Port != "" {
		port, err := strconv.Atoi(req.Port)
		if err != nil || port < 1 || port > 65535 {
			writeError(w, http.StatusBadRequest, errors.New("منفذ غير صالح"))
			return
		}
	}
	a.setSettings(req)
	writeJSON(w, http.StatusOK, a.mgr.State(req, a.version))
}

func (a *App) apiLayouts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, err := listLayouts(a.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *App) apiLayout(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/layouts/"), "/")
	switch r.Method {
	case http.MethodGet:
		data, err := loadLayout(a.root, id)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("تعذّرت قراءة الطلب"))
			return
		}
		saved, err := saveLayout(a.root, id, body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": saved})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) apiQuit(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = a.mgr.Stop()
		os.Exit(0)
	}()
}

func main() {
	var (
		portFlag    = flag.Int("port", 0, "منفذ اللوحة المحلية (0 = منفذ حر تلقائي)")
		noOpen      = flag.Bool("no-open", false, "لا تفتح المتصفح تلقائيًا")
		rootFlag    = flag.String("root", "", "مسار مشروع DOpad (يُكتشف تلقائيًا)")
		versionFlag = flag.Bool("version", false, "اطبع الإصدار واخرج")
	)
	flag.Parse()
	if *versionFlag {
		fmt.Println(version)
		return
	}

	root := findRoot(*rootFlag)
	mgr := NewManager(root)
	app := &App{
		root:     root,
		mgr:      mgr,
		token:    randomToken(),
		version:  version,
		settings: loadSettings(root),
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *portFlag))
	if err != nil {
		fmt.Fprintln(os.Stderr, "تعذّر تشغيل اللوحة المحلية:", err)
		os.Exit(1)
	}
	address := "http://" + listener.Addr().String() + "/"

	fmt.Printf("DOpad Panel %s\n", version)
	fmt.Printf("Root: %s\n", root)
	fmt.Printf("Open: %s\n", address)
	fmt.Println("Press Ctrl+C to stop the panel and the server it started.")

	if !*noOpen {
		go openBrowser(address)
	}

	server := &http.Server{
		Handler:           app.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, terminationSignals()...)
	go func() {
		<-signals
		fmt.Println("\nStopping...")
		_ = mgr.Stop()
		_ = server.Close()
		os.Exit(0)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "خطأ في اللوحة:", err)
		_ = mgr.Stop()
		os.Exit(1)
	}
}
