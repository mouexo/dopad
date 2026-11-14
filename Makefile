# اسم الملف التنفيذي
BINARY_NAME=DOpad
BINARY_WIN=$(BINARY_NAME).exe
# الدليل الذي يحتوي على وحدة Go (go.mod) والملف المصدري
SERVER_DIR=server

# لوحة التحكم المحلية (Go + web مضمّنة عبر go:embed)
PANEL_NAME=dopad-panel
PANEL_WIN=$(PANEL_NAME).exe
PANEL_DIR=gui/panel
PANEL_VERSION ?= $(V)
PANEL_LDFLAGS=-s -w -X main.version=$(PANEL_VERSION)

# إصدار الحزم: من آخر وسم في Git، ويمكن تجاوزه: make dist V=1.0.0
V ?= $(or $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//'),0.0.0)
DIST_DIR ?= dist

# وسوم البناء: webembed عند وجود واجهة مضمّنة، وvigemdll عند وجود DLL.
# نجمعهما في متغيّر واحد لأن تمرير -tags مرتين يجعل الأخيرة تلغي الأولى.
WEBEMBED_TAG=$(if $(wildcard $(SERVER_DIR)/web/index.html),webembed)
VIGEM_TAG=$(if $(wildcard $(SERVER_DIR)/ViGEmClient.dll),vigemdll)
LINUX_TAGS=$(strip $(WEBEMBED_TAG))
WINDOWS_TAGS=$(strip $(WEBEMBED_TAG) $(VIGEM_TAG))
tagflag=$(if $(1),-tags "$(1)")

# حزم التوزيع: نفس اسم الثنائي داخل مجلدين منفصلين لكل نظام.
PKG=$(BINARY_NAME)-$(V)
LINUX_EMBED_DIR=$(DIST_DIR)/$(PKG)-linux
LINUX_WEB_DIR=$(DIST_DIR)/$(PKG)-linux+web
WIN_EMBED_DIR=$(DIST_DIR)/$(PKG)-windows
WIN_WEB_DIR=$(DIST_DIR)/$(PKG)-windows+web
# نسختا ويندوز تضمّان DLL إن وُجد، والمضمّنة تضمّ web دائمًا.
WIN_EMBED_TAGS=$(strip webembed $(VIGEM_TAG))
WIN_WEB_TAGS=$(strip $(VIGEM_TAG))

.PHONY: all build build-debug build-linux build-linux-debug build-panel build-windows setup dist dist-linux dist-windows

all: build

# بناء نسخة لينكس (افتراضي) ولوحة التحكم، وإنتاج الثنائيين في جذر المشروع
build: build-linux build-panel
build-debug: build-linux-debug

# بناء لينكس: إن وُجد $(SERVER_DIR)/web يُضمَّن تلقائيًا عبر الوسم webembed،
# وإلا يُبنى عاديًا ويحتاج web/ بجانب الثنائي.
build-linux:
	@echo "جاري بناء نسخة لينكس..."
	@if [ -f $(SERVER_DIR)/web/index.html ]; then echo "→ $(SERVER_DIR)/web موجود: سيُضمَّن داخل $(BINARY_NAME)"; else echo "→ $(SERVER_DIR)/web غير موجود: ضع web بجانب $(BINARY_NAME)"; fi
	cd $(SERVER_DIR) && CGO_ENABLED=0 go build $(call tagflag,$(LINUX_TAGS)) -trimpath -ldflags="-s -w" -o ../$(BINARY_NAME) .
	@echo "تم البناء بنجاح: ./$(BINARY_NAME)"

build-linux-debug:
	@echo "جاري بناء نسخة لينكس..."
	@if [ -f $(SERVER_DIR)/web/index.html ]; then echo "→ $(SERVER_DIR)/web موجود: سيُضمَّن داخل $(BINARY_NAME)"; else echo "→ $(SERVER_DIR)/web غير موجود: ضع web بجانب $(BINARY_NAME)"; fi
	cd $(SERVER_DIR) && CGO_ENABLED=0 go build $(call tagflag,$(LINUX_TAGS)) -trimpath -o ../$(BINARY_NAME) .
	@echo "تم البناء بنجاح: ./$(BINARY_NAME)"

# بناء لوحة التحكم المحلية بجانب DOpad.
build-panel:
	@echo "جاري بناء لوحة التحكم..."
	cd $(PANEL_DIR) && CGO_ENABLED=0 go build -trimpath -ldflags "$(PANEL_LDFLAGS)" -o ../../$(PANEL_NAME) .
	@echo "تم البناء بنجاح: ./$(PANEL_NAME)"


# بناء نسخة ويندوز (استصلاحي على لينكس عبر cross-compilation → DOpad.exe).
# على ويندوز، ثّبت برنامج تشغيل ViGEmBus أولاً (انظر README — قسم Windows).
build-windows:
	@echo "جاري بناء نسخة ويندوز (GOOS=windows GOARCH=amd64)..."
	@if [ -f $(SERVER_DIR)/ViGEmClient.dll ]; then \
		echo "→ ViGEmClient.dll موجود: سيُضمَّن داخل $(BINARY_WIN) (ملف واحد جاهز للتوزيع)"; \
	else \
		echo "→ تنبيه: ضع ViGEmClient.dll (x64) في $(SERVER_DIR)/ ليُضمَّن داخل الملف، وإلا ضعه بجانب $(BINARY_WIN)"; \
	fi
	cd $(SERVER_DIR) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(call tagflag,$(WINDOWS_TAGS)) -trimpath -ldflags="-s -w" -o ../$(BINARY_WIN) .
	@echo "تم البناء بنجاح: ./$(BINARY_WIN)"

# ===================== حزم التوزيع =====================
# make dist ينتج أربعة مجلدات في $(DIST_DIR) (افتراضيًا dist/):
#   DOpad-<ver>-linux        : الواجهة مضمّنة + dopad-panel + web/controls.js + layouts + الإشعارات
#   DOpad-<ver>-linux+web    : web خارجي    + dopad-panel + layouts + الإشعارات
#   DOpad-<ver>-windows      : الواجهة و ViGEmClient.dll مضمّنان + dopad-panel.exe + web/controls.js
#   DOpad-<ver>-windows+web  : web خارجي، و DLL مضمّن + dopad-panel.exe
# اسم الثنائي داخل كل مجلد يبقى DOpad (أو DOpad.exe)، واللوحة dopad-panel (أو .exe).
# المجلدات تُعاد إنشاؤها في كل مرة، فلا تضع فيها تعديلات يدوية.
dist: dist-linux dist-windows

dist-linux: dist-linux-embed dist-linux-web
dist-windows: dist-windows-embed dist-windows-web

$(LINUX_EMBED_DIR) $(LINUX_WEB_DIR) $(WIN_EMBED_DIR) $(WIN_WEB_DIR):
	@rm -rf "$@"
	@mkdir -p "$@"

# البناء المضمّن يجري في نسخة مؤقتة من server/ مع web/، كي يعمل //go:embed
# دون لمس $(SERVER_DIR)/web الخاص بك (وهو مجلد مولّد ومتجاهَل في Git).
dist-linux-embed: $(LINUX_EMBED_DIR)
	@echo "→ حزمة لينكس (الواجهة مضمّنة)..."
	@set -e; \
	stage="$(CURDIR)/$(DIST_DIR)/.stage-linux"; \
	rm -rf "$$stage"; mkdir -p "$$stage"; \
	cp -r $(SERVER_DIR)/. "$$stage/"; \
	rm -rf "$$stage/web"; cp -r web "$$stage/web"; \
	trap 'rm -rf "$$stage"' EXIT; \
	cd "$$stage" && CGO_ENABLED=0 go build -tags webembed -trimpath -ldflags="-s -w" -o "$(CURDIR)/$(LINUX_EMBED_DIR)/$(BINARY_NAME)" .
	@echo "→ إضافة لوحة التحكم..."
	cd $(PANEL_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(PANEL_LDFLAGS)" -o "$(CURDIR)/$(LINUX_EMBED_DIR)/$(PANEL_NAME)" .
	@mkdir -p "$(LINUX_EMBED_DIR)/web"
	@cp web/controls.js "$(LINUX_EMBED_DIR)/web/"
	@cp -r layouts "$(LINUX_EMBED_DIR)/"
	@cp docs/THIRD_PARTY_NOTICES.md "$(LINUX_EMBED_DIR)/"
	@echo "   $(LINUX_EMBED_DIR)/"

dist-linux-web: $(LINUX_WEB_DIR)
	@echo "→ حزمة لينكس (web خارجي)..."
	cd $(SERVER_DIR) && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$(CURDIR)/$(LINUX_WEB_DIR)/$(BINARY_NAME)" .
	@echo "→ إضافة لوحة التحكم..."
	cd $(PANEL_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(PANEL_LDFLAGS)" -o "$(CURDIR)/$(LINUX_WEB_DIR)/$(PANEL_NAME)" .
	@cp -r web "$(LINUX_WEB_DIR)/"
	@cp -r layouts "$(LINUX_WEB_DIR)/"
	@cp docs/THIRD_PARTY_NOTICES.md "$(LINUX_WEB_DIR)/"
	@echo "   $(LINUX_WEB_DIR)/"

dist-windows-embed: $(WIN_EMBED_DIR)
	@echo "→ حزمة ويندوز (الواجهة و DLL مضمّنان)..."
	@if [ ! -f $(SERVER_DIR)/ViGEmClient.dll ]; then echo "⚠️  ViGEmClient.dll غير موجود: ضعه بجانب DOpad.exe يدويًا."; fi
	@set -e; \
	stage="$(CURDIR)/$(DIST_DIR)/.stage-windows"; \
	rm -rf "$$stage"; mkdir -p "$$stage"; \
	cp -r $(SERVER_DIR)/. "$$stage/"; \
	rm -rf "$$stage/web"; cp -r web "$$stage/web"; \
	trap 'rm -rf "$$stage"' EXIT; \
	cd "$$stage" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(call tagflag,$(WIN_EMBED_TAGS)) -trimpath -ldflags="-s -w" -o "$(CURDIR)/$(WIN_EMBED_DIR)/$(BINARY_WIN)" .
	@echo "→ إضافة لوحة التحكم..."
	cd $(PANEL_DIR) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(PANEL_LDFLAGS)" -o "$(CURDIR)/$(WIN_EMBED_DIR)/$(PANEL_WIN)" .
	@mkdir -p "$(WIN_EMBED_DIR)/web"
	@cp web/controls.js "$(WIN_EMBED_DIR)/web/"
	@cp -r layouts "$(WIN_EMBED_DIR)/"
	@cp docs/THIRD_PARTY_NOTICES.md "$(WIN_EMBED_DIR)/"
	@echo "   $(WIN_EMBED_DIR)/"

dist-windows-web: $(WIN_WEB_DIR)
	@echo "→ حزمة ويندوز (web خارجي)..."
	@if [ ! -f $(SERVER_DIR)/ViGEmClient.dll ]; then echo "⚠️  ViGEmClient.dll غير موجود: ضعه بجانب DOpad.exe يدويًا."; fi
	cd $(SERVER_DIR) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(call tagflag,$(WIN_WEB_TAGS)) -trimpath -ldflags="-s -w" -o "$(CURDIR)/$(WIN_WEB_DIR)/$(BINARY_WIN)" .
	@echo "→ إضافة لوحة التحكم..."
	cd $(PANEL_DIR) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(PANEL_LDFLAGS)" -o "$(CURDIR)/$(WIN_WEB_DIR)/$(PANEL_WIN)" .
	@cp -r web "$(WIN_WEB_DIR)/"
	@cp -r layouts "$(WIN_WEB_DIR)/"
	@cp docs/THIRD_PARTY_NOTICES.md "$(WIN_WEB_DIR)/"
	@echo "   $(WIN_WEB_DIR)/"

# إعداد صلاحيات uinput على لينكس (يتطلب sudo).
# على ويندوز: سّرّف برنامج تشغيل ViGEmBus بدلاً من هذا الأمر (لا udev هناك).
setup:
	@echo "جاري إعداد صلاحيات uinput..."
	@echo 'KERNEL=="uinput", MODE="0660", GROUP="input", OPTIONS+="static_node=uinput"' | sudo tee /etc/udev/rules.d/99-uinput.rules > /dev/null
	@sudo usermod -aG input $(USER)
	@sudo udevadm control --reload-rules && sudo udevadm trigger
	@echo "------------------------------------------------------"
	@echo "تم إعداد الصلاحيات بنجاح."
	@echo "ملاحظة: قد تحتاج لتسجيل الخروج والعودة (Log out/in) لتفعيل الصلاحيات."
	@echo "------------------------------------------------------"
