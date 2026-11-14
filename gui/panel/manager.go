package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type LogLine struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

type State struct {
	Running        bool     `json:"running"`
	Starting       bool     `json:"starting"`
	Mode           string   `json:"mode"`
	Port           string   `json:"port"`
	URL            string   `json:"url"`
	PID            int      `json:"pid"`
	StartedAt      string   `json:"startedAt"`
	ExitCode       int      `json:"exitCode"`
	Binary         string   `json:"binary"`
	DetectedBinary string   `json:"detectedBinary"`
	Settings       Settings `json:"settings"`
	Platform       string   `json:"platform"`
	IsRoot         bool     `json:"isRoot"`
	HasPkexec      bool     `json:"hasPkexec"`
	HasSudo        bool     `json:"hasSudo"`
	CanSelf        bool     `json:"canSelf"`
	ProjectRoot    string   `json:"projectRoot"`
	Version        string   `json:"version"`
}

type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		index := bytes.IndexByte(w.buf, '\n')
		if index < 0 {
			break
		}
		line := string(w.buf[:index])
		w.buf = w.buf[index+1:]
		w.fn(line)
	}
	if len(w.buf) > 1<<20 {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-64*1024:]...)
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		line := string(w.buf)
		w.buf = nil
		w.fn(line)
	}
}

type Manager struct {
	mu         sync.Mutex
	opMu       sync.Mutex
	root       string
	cmd        *exec.Cmd
	writer     *lineWriter
	mode       string
	port       string
	binary     string
	password   string
	startedAt  time.Time
	url        string
	pid        int
	exitCode   int
	running    bool
	stopping   bool
	logs       []LogLine
	seq        int64
	waitDone   chan struct{}
	maxLogLine int
}

func NewManager(root string) *Manager {
	return &Manager{root: root, maxLogLine: 2000}
}

var (
	urlRe  = regexp.MustCompile(`https?://[^\s"'<>]+`)
	portRe = regexp.MustCompile(`(?i)(?:المنفذ|port)\s*[:=]?\s*(\d{2,5})`)
)

func qrLike(line string) bool {
	text := strings.TrimSpace(line)
	if text == "" {
		return false
	}
	blocks := 0
	for _, ch := range text {
		if strings.ContainsRune("█▀▄▌▐░▒▓", ch) {
			blocks++
		}
	}
	return blocks >= 6 && blocks*4 >= utf8.RuneCountInString(text)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func withPortEnv(port string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, "DOPAD_PORT=") {
			out = append(out, item)
		}
	}
	if port != "" {
		out = append(out, "DOPAD_PORT="+port)
	}
	return out
}

// selfScript wraps the DOpad binary in a tiny shell script so DOPAD_PORT
// survives sudo/pkexec environment sanitising, and so the process keeps the
// panel's working directory.
func selfScript(root, binary, port string) string {
	script := "exec " + shellQuote(binary) + " -s"
	if port != "" {
		script = "DOPAD_PORT=" + shellQuote(port) + " " + script
	}
	return "cd " + shellQuote(root) + " && " + script
}

func (m *Manager) Start(mode, port, binary, password string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "wifi", "bt", "self":
	default:
		return errors.New("وضع تشغيل غير معروف")
	}
	if mode == "self" && runtime.GOOS != "linux" {
		return errors.New("وضع Self متاح على لينكس فقط")
	}
	port = strings.TrimSpace(port)
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("منفذ غير صالح")
		}
	}

	m.mu.Lock()
	already := m.running
	m.mu.Unlock()
	if already {
		return errors.New("الخادم يعمل بالفعل")
	}

	target, err := resolveBinary(m.root, binary)
	if err != nil {
		return err
	}

	program := target
	args := []string{}
	switch mode {
	case "bt":
		args = append(args, "-b")
	case "self":
		args = append(args, "-s")
	}

	stdin := ""
	if mode == "self" && runtime.GOOS == "linux" && !isRootUser() {
		if password != "" {
			program = "sudo"
			args = []string{"-S", "-p", "", "/bin/sh", "-c", selfScript(m.root, target, port)}
			stdin = password + "\n"
		} else if hasExecutable("pkexec") {
			program = "pkexec"
			args = []string{"/bin/sh", "-c", selfScript(m.root, target, port)}
		} else if hasExecutable("sudo") {
			program = "sudo"
			args = []string{"-n", "/bin/sh", "-c", selfScript(m.root, target, port)}
		} else {
			return errors.New("وضع Self يحتاج صلاحيات root. شغّل اللوحة بالأمر sudo ./dopad-panel أو أدخل كلمة المرور.")
		}
	}

	cmd := exec.Command(program, args...)
	cmd.Dir = m.root
	cmd.Env = withPortEnv(port)
	configureCmd(cmd)

	writer := &lineWriter{fn: m.addLog}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	m.mu.Lock()
	m.running = true
	m.mode = mode
	m.port = port
	m.binary = target
	m.password = password
	m.startedAt = time.Now()
	m.url = ""
	m.exitCode = 0
	m.pid = 0
	m.stopping = false
	m.cmd = cmd
	m.writer = writer
	m.waitDone = make(chan struct{})
	m.mu.Unlock()

	m.addLog(fmt.Sprintf("$ %s %s", program, strings.Join(args, " ")))
	m.addLog("[جارٍ تشغيل الخادم...]")

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.running = false
		m.cmd = nil
		m.password = ""
		done := m.waitDone
		m.waitDone = nil
		m.mu.Unlock()
		if done != nil {
			close(done)
		}
		return fmt.Errorf("تعذّر تشغيل الخادم: %w", err)
	}

	m.mu.Lock()
	m.pid = cmd.Process.Pid
	m.mu.Unlock()

	go m.wait(cmd, writer)
	return nil
}

func (m *Manager) wait(cmd *exec.Cmd, writer *lineWriter) {
	err := cmd.Wait()
	writer.Flush()

	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}

	m.mu.Lock()
	stopping := m.stopping
	m.stopping = false
	if stopping {
		code = 0
	}
	m.exitCode = code
	m.running = false
	m.pid = 0
	m.password = ""
	m.cmd = nil
	done := m.waitDone
	m.waitDone = nil
	m.mu.Unlock()

	if stopping {
		m.addLog("[أُوقف الخادم]")
	} else {
		suffix := ""
		if code == -1 {
			suffix = " (crash)"
		}
		m.addLog(fmt.Sprintf("[انتهى الخادم، رمز الخروج %d%s]", code, suffix))
	}
	if done != nil {
		close(done)
	}
}

func (m *Manager) addLog(line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" || qrLike(line) {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if match := urlRe.FindString(line); match != "" {
		match = strings.TrimRight(match, ".,);]")
		if parsed, err := url.Parse(match); err == nil && parsed.Host != "" {
			m.url = parsed.Scheme + "://" + parsed.Host
			if parsed.Port() != "" {
				m.port = parsed.Port()
			}
		}
	}
	if match := portRe.FindStringSubmatch(line); len(match) == 2 {
		m.port = match[1]
	}

	m.seq++
	m.logs = append(m.logs, LogLine{Seq: m.seq, Text: line})
	if len(m.logs) > m.maxLogLine {
		m.logs = append([]LogLine(nil), m.logs[len(m.logs)-m.maxLogLine:]...)
	}
}

func (m *Manager) Logs(after int64) (int64, []LogLine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogLine, 0)
	for _, line := range m.logs {
		if line.Seq > after {
			out = append(out, line)
		}
	}
	return m.seq, out
}

func (m *Manager) ClearLogs() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = nil
	return m.seq
}

// FetchQR asks the running DOpad server for its latest QR PNG. DOpad exposes
// /qr on every interface, so loopback works in Wi-Fi, Bluetooth, and Self mode.
func (m *Manager) FetchQR() ([]byte, error) {
	m.mu.Lock()
	running := m.running
	port := m.port
	m.mu.Unlock()

	if !running || port == "" {
		return nil, errors.New("الخادم لا يعمل")
	}

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/qr")
	if err != nil {
		return nil, errors.New("تعذّر جلب رمز QR من الخادم")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errors.New("لم يتصل هاتف بعد — لا يوجد رمز QR حتى الآن")
	default:
		return nil, fmt.Errorf("الخادم أعاد الحالة %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "image/png") {
		return nil, errors.New("استجابة رمز QR غير متوقعة")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, errors.New("تعذّرت قراءة رمز QR")
	}
	if len(data) == 0 {
		return nil, errors.New("رمز QR فارغ")
	}
	return data, nil
}

func killCommand() string {
	path, err := exec.LookPath("kill")
	if err != nil || path == "" {
		path = "/bin/kill"
	}
	return path
}

func sudoKill(pid int, sig, password, dir string) error {
	kill := killCommand()
	var args []string
	if password == "" {
		args = []string{"-n", kill, "-" + sig, "-" + strconv.Itoa(pid)}
	} else {
		args = []string{"-S", "-p", "", kill, "-" + sig, "-" + strconv.Itoa(pid)}
	}
	cmd := exec.Command("sudo", args...)
	cmd.Dir = dir
	if password != "" {
		cmd.Stdin = strings.NewReader(password + "\n")
	}
	return cmd.Run()
}

func pkexecKill(pid int, sig string) error {
	kill := killCommand()
	cmd := exec.Command("pkexec", kill, "-"+sig, "-"+strconv.Itoa(pid))
	return cmd.Run()
}

func (m *Manager) Stop() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	pid := m.pid
	mode := m.mode
	password := m.password
	done := m.waitDone
	m.stopping = true
	m.mu.Unlock()

	m.addLog("[إيقاف الخادم...]")

	selfWrapper := mode == "self" && runtime.GOOS == "linux" && !isRootUser()
	if selfWrapper {
		switch {
		case password != "":
			if err := sudoKill(pid, "TERM", password, m.root); err != nil {
				return fmt.Errorf("تعذّر إيقاف الخادم: %w", err)
			}
		case hasExecutable("pkexec"):
			if err := pkexecKill(pid, "TERM"); err != nil {
				return fmt.Errorf("تعذّر إيقاف الخادم: %w", err)
			}
		case hasExecutable("sudo"):
			if err := sudoKill(pid, "TERM", "", m.root); err != nil {
				return fmt.Errorf("تعذّر إيقاف الخادم: %w", err)
			}
		}
	} else if err := killTree(pid, false); err != nil {
		return fmt.Errorf("تعذّر إيقاف الخادم: %w", err)
	}

	if done != nil {
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			m.addLog("[الخادم لم يتوقف، إرسال SIGKILL]")
			if selfWrapper {
				switch {
				case password != "":
					_ = sudoKill(pid, "KILL", password, m.root)
				case hasExecutable("pkexec"):
					_ = pkexecKill(pid, "KILL")
				case hasExecutable("sudo"):
					_ = sudoKill(pid, "KILL", "", m.root)
				}
			} else {
				_ = killTree(pid, true)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}

	m.mu.Lock()
	m.password = ""
	m.mu.Unlock()
	return nil
}

func (m *Manager) State(settings Settings, version string) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := State{
		Running:        m.running,
		Mode:           settings.Mode,
		Port:           settings.Port,
		PID:            m.pid,
		ExitCode:       m.exitCode,
		Binary:         settings.Binary,
		DetectedBinary: detectBinary(m.root),
		Settings:       settings,
		Platform:       runtime.GOOS,
		IsRoot:         isRootUser(),
		HasPkexec:      hasExecutable("pkexec"),
		HasSudo:        hasExecutable("sudo"),
		CanSelf:        runtime.GOOS == "linux",
		ProjectRoot:    m.root,
		Version:        version,
	}
	if m.running {
		state.Mode = m.mode
		state.Port = m.port
		state.Binary = m.binary
		state.URL = m.url
		state.StartedAt = m.startedAt.Format(time.RFC3339)
		state.Starting = m.url == "" && time.Since(m.startedAt) < 15*time.Second
	}
	return state
}

func (m *Manager) Root() string { return m.root }

func (m *Manager) Join(parts ...string) string {
	return filepath.Join(append([]string{m.root}, parts...)...)
}
