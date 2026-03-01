export PATH := /usr/local/Cellar/go/1.26.0/bin:$(PATH)

VERSION  := 1.7.5
BINARY   := tetora
INSTALL  := $(HOME)/.tetora/bin
LDFLAGS  := -s -w -X main.tetoraVersion=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build dev reload install clean release test bump

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

dev:
	go build -ldflags "$(LDFLAGS)" -o $(INSTALL)/$(BINARY) .

reload: dev
	$(INSTALL)/$(BINARY) stop 2>/dev/null || true
	sleep 1
	$(INSTALL)/$(BINARY) start 2>/dev/null || true
	@echo "Reloaded v$(VERSION)"

install: build
	@mkdir -p $(INSTALL)
	cp $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"
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
		echo "Run: source $$SHELL_RC  (or restart your shell)"; \
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
	$(INSTALL)/$(BINARY) stop 2>/dev/null || true; \
	sleep 1; \
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
