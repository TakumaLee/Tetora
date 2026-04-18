export PATH := /usr/local/Cellar/go/1.26.0/bin:$(PATH)

VERSION  := 2.2.7
BINARY   := tetora
INSTALL  := $(HOME)/.tetora/bin
LDFLAGS  := -s -w -X main.tetoraVersion=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build dev reload install clean release release-stable test bump bump-force dashboard local-install

DASH_PARTS := dashboard/head.html dashboard/style.css dashboard/body.html \
	dashboard/core.js dashboard/views.js dashboard/workers.js \
	dashboard/modals.js dashboard/tasks.js dashboard/dispatch.js \
	dashboard/agents.js dashboard/charts.js dashboard/workflow-editor.js dashboard/capabilities.js dashboard/team-builder.js dashboard/store.js dashboard/office.js dashboard/docs.js dashboard/pwa.js dashboard/war-room.js \
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
		    dashboard/agents.js dashboard/charts.js dashboard/workflow-editor.js dashboard/capabilities.js dashboard/team-builder.js dashboard/store.js dashboard/office.js dashboard/docs.js dashboard/pwa.js dashboard/war-room.js; \
		echo '</script>'; \
		cat dashboard/foot.html; \
	} > dashboard.html
	@echo "dashboard.html built ($$(wc -l < dashboard.html | tr -d ' ') lines)"

build: dashboard
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .
	@codesign -s - -f -i com.takumalee.tetora $(BINARY) 2>/dev/null || true

dev: dashboard
	go build -ldflags "$(LDFLAGS)" -o $(INSTALL)/$(BINARY) .
	@codesign -s - -f -i com.takumalee.tetora $(INSTALL)/$(BINARY) 2>/dev/null || true

PLIST := $(HOME)/Library/LaunchAgents/com.tetora.daemon.plist

reload: dev
	@# Unload launchd first to prevent KeepAlive from re-spawning during binary replacement.
	@if [ -f "$(PLIST)" ]; then launchctl bootout gui/$$(id -u)/com.tetora.daemon 2>/dev/null || true; fi
	@$(INSTALL)/$(BINARY) stop 2>/dev/null || true
	@sleep 1
	@pgrep -f "tetora serve" | xargs kill -9 2>/dev/null || true
	@sleep 1
	@if [ -f "$(PLIST)" ]; then \
		launchctl bootstrap gui/$$(id -u) "$(PLIST)" 2>/dev/null || true; \
	else \
		$(INSTALL)/$(BINARY) start 2>/dev/null || true; \
	fi
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

_bump_check_running_workflows:
	@ADDR=$$(python3 -c "import json; c=json.load(open('$(HOME)/.tetora/config.json')); print(c.get('listenAddr','127.0.0.1:8991'))" 2>/dev/null || echo "127.0.0.1:8991"); \
	HEALTH=$$(curl -sf --max-time 3 "http://$$ADDR/healthz" 2>/dev/null) || true; \
	if [ -n "$$HEALTH" ]; then \
		RUNNING=$$(echo "$$HEALTH" | python3 -c \
			"import json,sys; d=json.load(sys.stdin); r=d.get('dispatch',{}).get('running',0); \
			tasks=d.get('dispatch',{}).get('tasks') or []; \
			[print('  ' + t.get('name','?') + ' [' + t.get('agent','?') + ']') for t in tasks]; \
			sys.exit(1 if r > 0 else 0)" 2>/dev/null); \
		if [ $$? -ne 0 ]; then \
			echo ""; \
			echo "⚠ WARNING: Tasks are currently running:"; \
			echo "$$RUNNING"; \
			echo ""; \
			echo "Bumping now will kill them mid-run."; \
			echo "  Use 'make bump-force' to proceed anyway."; \
			echo "  Or wait for them to finish and re-run 'make bump'."; \
			echo ""; \
			exit 1; \
		fi; \
	fi; \
	RUNS=$$(curl -sf --max-time 3 "http://$$ADDR/workflow-runs" 2>/dev/null) || true; \
	if [ -n "$$RUNS" ]; then \
		RUNNING=$$(echo "$$RUNS" | python3 -c \
			"import json,sys; runs=[r for r in json.load(sys.stdin) if r.get('status')=='running']; \
			[print('  ' + r.get('workflowName','?') + ' / ' + r['id'][:8].upper()) for r in runs]; \
			sys.exit(1 if runs else 0)" 2>/dev/null); \
		if [ $$? -ne 0 ]; then \
			echo ""; \
			echo "⚠ WARNING: Workflows are currently running:"; \
			echo "$$RUNNING"; \
			echo ""; \
			echo "Bumping now will kill them mid-run."; \
			echo "  Use 'make bump-force' to proceed anyway."; \
			echo "  Or wait for them to finish and re-run 'make bump'."; \
			echo ""; \
			exit 1; \
		fi; \
	fi

bump: _bump_check_running_workflows dashboard
	@CURRENT=$(VERSION); \
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
	$(INSTALL)/$(BINARY) restart 2>&1 || true; \
	echo "v$$NEXT installed and reloaded"

bump-force: dashboard
	@CURRENT=$(VERSION); \
	MAJOR=$$(echo $$CURRENT | cut -d. -f1); \
	MINOR=$$(echo $$CURRENT | cut -d. -f2); \
	PATCH=$$(echo $$CURRENT | cut -d. -f3); \
	DEV=$$(echo $$CURRENT | cut -d. -f4 -s); \
	if [ -z "$$DEV" ]; then NEXT="$$MAJOR.$$MINOR.$$PATCH.1"; \
	else NEXT="$$MAJOR.$$MINOR.$$PATCH.$$((DEV+1))"; fi; \
	echo "Bumping $$CURRENT → $$NEXT (dev) [force — skipping workflow check]"; \
	sed -i '' "s/^VERSION  := .*/VERSION  := $$NEXT/" Makefile; \
	go build -ldflags "-s -w -X main.tetoraVersion=$$NEXT" -o $(INSTALL)/$(BINARY) .; \
	codesign -s - -f -i com.takumalee.tetora $(INSTALL)/$(BINARY) 2>/dev/null || true; \
	$(INSTALL)/$(BINARY) restart 2>&1 || true; \
	echo "v$$NEXT installed and reloaded"

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

local-install: build
	@$(INSTALL)/$(BINARY) stop 2>/dev/null || true
	@sleep 1
	@if lsof -ti :8991 >/dev/null 2>&1; then \
		lsof -ti :8991 | xargs kill -9 2>/dev/null || true; \
		sleep 1; \
	fi
	@cp $(BINARY) $(INSTALL)/$(BINARY)
	@codesign -s - -f -i com.takumalee.tetora $(INSTALL)/$(BINARY) 2>/dev/null || true
	@$(INSTALL)/$(BINARY) start 2>/dev/null || true
	@echo "Local daemon updated to v$(VERSION)"

release: local-install
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

# Stable release workflow. Usage: make release-stable VERSION=2.2.6
# Steps: validate → clean-tree check → running-workflow check → tests → bump Makefile → cross-build → checksum → local-install → git commit + tag (no push).
release-stable: _bump_check_running_workflows
	@if ! echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "ERROR: VERSION must be stable format x.y.z (e.g. VERSION=2.2.6)"; \
		echo "  Got:   '$(VERSION)'"; \
		echo "  Usage: make release-stable VERSION=2.2.6"; \
		exit 1; \
	fi
	@if ! git diff --quiet || ! git diff --cached --quiet; then \
		echo "ERROR: tracked files have uncommitted changes — commit or stash first"; \
		git status --short; \
		exit 1; \
	fi
	@if git rev-parse "v$(VERSION)" >/dev/null 2>&1; then \
		echo "ERROR: git tag v$(VERSION) already exists"; \
		exit 1; \
	fi
	@echo "==> Running tests..."
	@go test ./... || { echo "ERROR: tests failed, aborting release"; exit 1; }
	@echo "==> Updating Makefile VERSION → $(VERSION)"
	@sed -i '' "s/^VERSION  := .*/VERSION  := $(VERSION)/" Makefile
	@echo "==> Building cross-platform binaries..."
	@rm -rf dist
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  - $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" \
			-o dist/$(BINARY)-$$os-$$arch$$ext . || { echo "ERROR: build failed for $$os/$$arch"; exit 1; }; \
	done
	@echo "==> Generating SHA256SUMS..."
	@cd dist && shasum -a 256 $(BINARY)-* > SHA256SUMS
	@echo "==> Installing to local daemon..."
	@$(MAKE) --no-print-directory local-install
	@echo "==> Creating git commit + tag..."
	@git add Makefile
	@git commit -m "release: v$(VERSION)"
	@git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	@echo ""
	@echo "✓ Release v$(VERSION) ready"
	@ls -lh dist/
	@echo ""
	@echo "Next steps (run manually):"
	@echo "  git push origin main"
	@echo "  git push origin v$(VERSION)"
	@echo "  gh release create v$(VERSION) dist/* --generate-notes"
