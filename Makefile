# اسم الملف التنفيذي
BINARY_NAME=DOpad
BINARY_WIN=$(BINARY_NAME).exe
# الدليل الذي يحتوي على وحدة Go (go.mod) والملف المصدري
SERVER_DIR=server

.PHONY: all build build-linux build-windows run run-self setup clean

all: build
all: build-debug

# بناء نسخة لينكس (افتراضي) وإنتاج الثنائي في جذر المشروع
build: build-linux
build-debug: build-linux-debug

build-linux:
	@echo "جاري بناء نسخة لينكس..."
	cd $(SERVER_DIR) && go build -ldflags="-s -w" -o ../$(BINARY_NAME) .
	@echo "تم البناء بنجاح: ./$(BINARY_NAME)"

build-linux-debug:
	@echo "جاري بناء نسخة لينكس..."
	cd $(SERVER_DIR) && go build -o ../$(BINARY_NAME) .
	@echo "تم البناء بنجاح: ./$(BINARY_NAME)"


# بناء نسخة ويندوز (استصلاحي على لينكس عبر cross-compilation → DOpad.exe).
# على ويندوز، ثّبت برنامج تشغيل ViGEmBus أولاً (انظر README — قسم Windows).
build-windows:
	@echo "جاري بناء نسخة ويندوز (GOOS=windows GOARCH=amd64)..."
	@if [ -f $(SERVER_DIR)/ViGEmClient.dll ]; then \
		echo "→ ViGEmClient.dll موجود: سيُضمَّن داخل $(BINARY_WIN) (ملف واحد جاهز للتوزيع)"; \
		cd $(SERVER_DIR) && GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -tags vigemdll -o ../$(BINARY_WIN) .; \
	else \
		echo "→ تنبيه: ضع ViGEmClient.dll (x64) في $(SERVER_DIR)/ ليُضمَّن داخل الملف، وإلا ضعه بجانب $(BINARY_WIN)"; \
		cd $(SERVER_DIR) && GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o ../$(BINARY_WIN) .; \
	fi
	@echo "تم البناء بنجاح: ./$(BINARY_WIN)"

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

# تشغيل البرنامج (نسخة لينكس) — يبدأ على HTTP.
# يعمل مع كل المتصفحات بما فيها Safari و Brave على الأيفون (بلا شهادة، بلا إعداد).
run: build
	./$(BINARY_NAME)

# تشغيل نقطة الوصل المباشرة (الحاسوب = hotspot، بلا راوتر/إنترنت) — يتطلّب جذرًا.
# وضع -s غير مدعوم على ويندوز.
run-self: build
	@echo "تشغيل نقطة الوصل المباشرة (يتطلّب sudo …)"
	sudo -E ./$(BINARY_NAME) -s

# تنظيف الملفات الناتجة
clean:
	@echo "جاري التنظيف..."
	rm -f $(BINARY_NAME) $(BINARY_WIN) dopad.crt dopad.key
	@echo "تم التنظيف."
