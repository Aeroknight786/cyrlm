VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build test vet dist

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/trialm ./cmd/trialm

test:
	go test ./...

vet:
	go vet ./...
	test -z "$$(gofmt -l .)"

# Builds release archives into dist/: trialm_<os>_<arch>.tar.gz (.zip on
# Windows) plus checksums.txt. Staging folders dist/trialm_<os>_<arch>/ are
# kept (dist/ is git-ignored).
dist:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=".exe"; \
		dir=dist/trialm_$${os}_$${arch}; mkdir -p $$dir; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/trialm$$ext ./cmd/trialm || exit 1; \
		cp README.md $$dir/; \
		if [ $$os = windows ]; then (cd dist && zip -qr trialm_$${os}_$${arch}.zip trialm_$${os}_$${arch}); \
		else tar -C dist -czf dist/trialm_$${os}_$${arch}.tar.gz trialm_$${os}_$${arch}; fi; \
		echo "built $$t"; \
	done
	@cd dist && (command -v sha256sum >/dev/null && sha256sum *.tar.gz *.zip || shasum -a 256 *.tar.gz *.zip) > checksums.txt
