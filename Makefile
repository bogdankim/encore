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

# strip -S drops the debug map, which holds this checkout's absolute paths.
bridge:
	cd bridge && swift build -c release --arch arm64
	strip -S "$$(cd bridge && swift build -c release --arch arm64 --show-bin-path)/encore-bridge"
	codesign --sign - --identifier $(BRIDGE_ID) \
		--requirements '=designated => identifier "$(BRIDGE_ID)"' \
		--force "$$(cd bridge && swift build -c release --arch arm64 --show-bin-path)/encore-bridge"
	mkdir -p internal/bridge/embed
	cp "$$(cd bridge && swift build -c release --arch arm64 --show-bin-path)/encore-bridge" internal/bridge/embed/encore-bridge

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

# A release tarball with the binary, LICENSE and README.md, for the Homebrew
# formula. -trimpath keeps local file paths out of the binary.
DIST_NAME := encore-$(VERSION)-darwin-arm64

dist: bridge
	rm -rf dist/$(DIST_NAME) dist/$(DIST_NAME).tar.gz
	mkdir -p dist/$(DIST_NAME)
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(DIST_NAME)/encore ./cmd/encore
	cp LICENSE README.md dist/$(DIST_NAME)/
	COPYFILE_DISABLE=1 tar -C dist/$(DIST_NAME) -czf dist/$(DIST_NAME).tar.gz encore LICENSE README.md
	rm -rf dist/$(DIST_NAME)
	@shasum -a 256 dist/$(DIST_NAME).tar.gz

clean:
	rm -rf bin dist bridge/.build internal/bridge/embed/encore-bridge
