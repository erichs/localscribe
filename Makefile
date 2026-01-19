GO ?= go

# Detect Go bin directory portably
GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
	GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: all build test fmt fmt-check vet revive gosec lint

all: lint test build

build:
	$(GO) build ./cmd/localscribe

test:
	$(GO) test ./...

# Format all Go files
fmt:
	@echo "Formatting Go files..."
	@$(GO) fmt ./...
	@echo "✅ All files formatted"

# Check if files need formatting (for CI)
fmt-check:
	@echo "Checking Go file formatting..."
	@test -z "$$(gofmt -l .)" || (echo "❌ Files need formatting. Run 'make fmt'" && gofmt -l . && exit 1)
	@echo "✅ All files properly formatted"

# Run go vet on the codebase
vet:
	@echo "Running go vet..."
	@$(GO) vet ./...
	@echo "✅ go vet passed"

# Run revive linter
revive:
	@echo "Running revive linter..."
	@if [ ! -f $(GOBIN)/revive ]; then \
		echo "revive not found. Installing..."; \
		$(GO) install github.com/mgechev/revive@latest; \
	fi
	@$(GOBIN)/revive ./...
	@echo "✅ Revive passed - no issues found!"

# Run security scanner
gosec:
	@echo "Running security scanner (gosec)..."
	@if [ ! -f $(GOBIN)/gosec ]; then \
		echo "gosec not found. Installing..."; \
		$(GO) install github.com/securego/gosec/v2/cmd/gosec@latest; \
	fi
	@$(GOBIN)/gosec ./...
	@echo "✅ Security scan passed - no issues found!"

lint: fmt-check vet revive gosec

# Build macOS .app bundle with microphone indicator support (ad-hoc signed)
APP_NAME := LocalScribe.app
APP_CONTENTS := $(APP_NAME)/Contents
APP_MACOS := $(APP_CONTENTS)/MacOS

define INFO_PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>localscribe</string>
    <key>CFBundleIdentifier</key>
    <string>com.localscribe</string>
    <key>CFBundleName</key>
    <string>LocalScribe</string>
    <key>CFBundleVersion</key>
    <string>1.0</string>
    <key>NSMicrophoneUsageDescription</key>
    <string>LocalScribe needs microphone access to transcribe audio.</string>
</dict>
</plist>
endef
export INFO_PLIST

.PHONY: app
app: build
	@echo "Creating macOS app bundle..."
	@mkdir -p $(APP_MACOS)
	@cp localscribe $(APP_MACOS)/
	@echo "$$INFO_PLIST" > $(APP_CONTENTS)/Info.plist
	@codesign --sign - --entitlements config/entitlements.plist --options runtime --force $(APP_NAME)
	@echo "Created $(APP_NAME) - run with: ./$(APP_NAME)/Contents/MacOS/localscribe"
