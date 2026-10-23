.PHONY: all bridge build run demo test install dist clean

all: build

# The bridge's TCC identity: bridge/Info.plist is linked into the binary, and the
# ad-hoc signature carries the same identifier. An ad-hoc signature's implicit
# designated requirement is its cdhash, which every build changes; the explicit
# one below names only the identifier, so a permission given to one build can
# match the next. TCC keys the helper by path, so encore always extracts it to
# the same one (internal/bridge/locate.go). Whether TCC honours this requirement
# for ad-hoc code is undocumented; see docs/design.md.
BRIDGE_ID := io.github.bogdankim.encore.bridge

# The helper is universal (arm64 + x86_64), so the one embedded copy runs on
# every Mac. strip -S drops the debug map, which holds this checkout's
# absolute paths.
BRIDGE_ARCHS := --arch arm64 --arch x86_64
BRIDGE_BIN = $$(cd bridge && swift build -c release $(BRIDGE_ARCHS) --show-bin-path)/encore-bridge

bridge:
	cd bridge && swift build -c release $(BRIDGE_ARCHS)
	strip -S "$(BRIDGE_BIN)"
	codesign --sign - --identifier $(BRIDGE_ID) \
		--requirements '=designated => identifier "$(BRIDGE_ID)"' \
		--force "$(BRIDGE_BIN)"
	mkdir -p internal/bridge/embed
	cp "$(BRIDGE_BIN)" internal/bridge/embed/encore-bridge

# The version printed by encore --version. A release sets it explicitly:
# make dist VERSION=0.1.0. Otherwise it comes from git, minus a leading "v".
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
LDFLAGS := -X main.version=$(VERSION)

build: bridge
	go build -ldflags "$(LDFLAGS)" -o bin/encore ./cmd/encore

run: build
	./bin/encore

demo: build
	./bin/encore --demo

test:
	go test ./...
	cd bridge && swift test

install: build
	mkdir -p $(HOME)/.local/bin
	install -m 0755 bin/encore $(HOME)/.local/bin/encore

# A release tarball with a universal binary (arm64 + x86_64), LICENSE and
# README.md, for the Homebrew formula. -trimpath keeps local file paths out of
# the binary. Both halves link with cgo, so the x86_64 one needs clang told
# its target, and MACOSX_DEPLOYMENT_TARGET keeps the minimum macOS at 14, the
# helper's (clang would otherwise stamp the SDK's version).
DIST_NAME := encore-$(VERSION)-darwin-universal
DIST_DIR := dist/$(DIST_NAME)

dist: bridge
	rm -rf $(DIST_DIR) $(DIST_DIR).tar.gz
	mkdir -p $(DIST_DIR)
	MACOSX_DEPLOYMENT_TARGET=14.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="clang -arch arm64" \
		go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(DIST_DIR)/encore-arm64 ./cmd/encore
	MACOSX_DEPLOYMENT_TARGET=14.0 CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" \
		go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(DIST_DIR)/encore-amd64 ./cmd/encore
	lipo -create -output $(DIST_DIR)/encore $(DIST_DIR)/encore-arm64 $(DIST_DIR)/encore-amd64
	rm $(DIST_DIR)/encore-arm64 $(DIST_DIR)/encore-amd64
	codesign --sign - --force $(DIST_DIR)/encore
	cp LICENSE README.md $(DIST_DIR)/
	COPYFILE_DISABLE=1 tar -C $(DIST_DIR) -czf $(DIST_DIR).tar.gz encore LICENSE README.md
	rm -rf $(DIST_DIR)
	@shasum -a 256 $(DIST_DIR).tar.gz

clean:
	rm -rf bin dist bridge/.build internal/bridge/embed/encore-bridge
