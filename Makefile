VERSION  := 1.0.0
BINARY   := tetora
INSTALL  := $(HOME)/.tetora/bin
LDFLAGS  := -s -w -X main.tetoraVersion=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build install clean release test

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

install: build
	@mkdir -p $(INSTALL)
	cp $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"
	@echo "Make sure $(INSTALL) is in your PATH"

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
