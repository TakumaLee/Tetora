export PATH := /usr/local/Cellar/go/1.26.0/bin:$(PATH)

VERSION  := 2.0.0.47
BINARY   := tetora
INSTALL  := $(HOME)/.tetora/bin
LDFLAGS  := -s -w -X main.tetoraVersion=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build dev reload install clean release test bump dashboard

DASH_PARTS := dashboard/head.html dashboard/style.css dashboard/body.html \
	dashboard/core.js dashboard/views.js dashboard/workers.js \
	dashboard/modals.js dashboard/tasks.js dashboard/dispatch.js \
	dashboard/agents.js dashboard/charts.js dashboard/workflow-editor.js dashboard/office.js dashboard/pwa.js \
	dashboard/foot.html

dashboard: $(DASH_PARTS)
	@{ \
		cat dashboard/head.html; \
		echo '<style>'; \
		cat dashboard/style.css; \
		echo '</style>'; \
		echo '</head>'; \
		echo '<body>'; \
		cat dashboard/body.html; \
		echo '<script>'; \
		cat dashboard/core.js dashboard/views.js dashboard/workers.js \
		    dashboard/modals.js dashboard/tasks.js dashboard/dispatch.js \
		    dashboard/agents.js dashboard/charts.js dashboard/workflow-editor.js dashboard/office.js dashboard/pwa.js; \
		echo '</script>'; \
		cat dashboard/foot.html; \
	} > dashboard.html
	@echo "dashboard.html built ($$(wc -l < dashboard.html | tr -d ' ') lines)"

build: dashboard
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

dev: dashboard
	go build -ldflags "$(LDFLAGS)" -o $(INSTALL)/$(BINARY) .
	@codesign -s - -f -i com.takumalee.tetora $(INSTALL)/$(BINARY) 2>/dev/null || true

reload: dev
	$(INSTALL)/$(BINARY) stop 2>/dev/null || true
	@sleep 1
	@if lsof -ti :8991 >/dev/null 2>&1; then \
		lsof -ti :8991 | xargs kill -9 2>/dev/null || true; \
		sleep 1; \
	fi
	$(INSTALL)/$(BINARY) start 2>/dev/null || true
	@echo "Reloaded v$(VERSION)"

install: build
	@mkdir -p $(INSTALL)
	$(INSTALL)/$(BINARY) stop 2>/dev/null || true
	@sleep 1
	@if lsof -ti :8991 >/dev/null 2>&1; then \
		echo "ERROR: port 8991 still in use after stop, force killing..."; \
		lsof -ti :8991 | xargs kill -9 2>/dev/null || true; \
		sleep 1; \
		if lsof -ti :8991 >/dev/null 2>&1; then \
			echo "FATAL: cannot free port 8991, aborting install"; \
			exit 1; \
		fi; \
	fi
	cp $(BINARY) $(INSTALL)/$(BINARY)
	@codesign -s - -f -i com.takumalee.tetora $(INSTALL)/$(BINARY) 2>/dev/null || true
	$(INSTALL)/$(BINARY) start 2>/dev/null || true
	@sleep 1
	@if lsof -ti :8991 >/dev/null 2>&1; then \
		echo "Installed v$(VERSION) and restarted (PID $$(lsof -ti :8991))"; \
	else \
		echo "WARNING: installed v$(VERSION) but daemon may not have started"; \
	fi
	@bash -c '\
		SHELL_RC=""; \
		case "$$(basename "$${SHELL:-/bin/bash}")" in \
			zsh) SHELL_RC="$$HOME/.zshrc" ;; \
			bash) if [ -f "$$HOME/.bash_profile" ]; then SHELL_RC="$$HOME/.bash_profile"; else SHELL_RC="$$HOME/.bashrc"; fi ;; \
		esac; \
		if [ -n "$$SHELL_RC" ] && ! grep -qF ".tetora/bin" "$$SHELL_RC" 2>/dev/null; then \
			echo "" >> "$$SHELL_RC"; \
			echo "# Tetora" >> "$$SHELL_RC"; \
			echo "export PATH=\"$$HOME/.tetora/bin:\$$PATH\"" >> "$$SHELL_RC"; \
			echo "Added PATH to $$SHELL_RC"; \
		fi; \
	'

bump:
	@CURRENT=$(VERSION); \
	PARTS=$$(echo $$CURRENT | tr '.' ' '); \
	MAJOR=$$(echo $$CURRENT | cut -d. -f1); \
	MINOR=$$(echo $$CURRENT | cut -d. -f2); \
	PATCH=$$(echo $$CURRENT | cut -d. -f3); \
	DEV=$$(echo $$CURRENT | cut -d. -f4 -s); \
	if [ -z "$$DEV" ]; then NEXT="$$MAJOR.$$MINOR.$$PATCH.1"; \
	else NEXT="$$MAJOR.$$MINOR.$$PATCH.$$((DEV+1))"; fi; \
	echo "Bumping $$CURRENT → $$NEXT (dev)"; \
	sed -i '' "s/^VERSION  := .*/VERSION  := $$NEXT/" Makefile; \
	go build -ldflags "-s -w -X main.tetoraVersion=$$NEXT" -o $(INSTALL)/$(BINARY) .; \
	codesign -s - -f -i com.takumalee.tetora $(INSTALL)/$(BINARY) 2>/dev/null || true; \
	$(INSTALL)/$(BINARY) stop 2>/dev/null || true; \
	sleep 1; \
	if lsof -ti :8991 >/dev/null 2>&1; then \
		lsof -ti :8991 | xargs kill -9 2>/dev/null || true; \
		sleep 1; \
	fi; \
	$(INSTALL)/$(BINARY) start 2>/dev/null || true; \
	echo "v$$NEXT installed and reloaded"

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

release:
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "Building $$os/$$arch..."; \
		GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" \
			-o dist/$(BINARY)-$$os-$$arch$$ext . ; \
	done
	@echo ""
	@echo "Release binaries:"
	@ls -lh dist/
