APP_NAME  := sshfortress
VERSION   ?= 2.0.0
BUILD_DIR := build
PKG       := sshfortress/internal/buildinfo
LDFLAGS   := -ldflags "-s -w -X $(PKG).Version=$(VERSION)"
GOFLAGS   := -trimpath
export CGO_ENABLED := 0

.PHONY: all build linux-amd64 linux-arm64 linux-armv7 clean test vet install

all: linux-amd64 linux-arm64 linux-armv7

build:
	go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) .

linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64 .

linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-armv7 .

checksums: all
	cd $(BUILD_DIR) && sha256sum $(APP_NAME)-linux-* > sha256sums.txt

vet:
	go vet ./...

test:
	go test -race -cover ./...

clean:
	rm -rf $(BUILD_DIR)

install: build
	install -m 0755 $(BUILD_DIR)/$(APP_NAME) /usr/local/bin/$(APP_NAME)
	@echo "Installed. First-time setup: sudo $(APP_NAME) setup"
