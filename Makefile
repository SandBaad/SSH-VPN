APP_NAME := sshfortress
VERSION := 1.0.0
BUILD_DIR := build
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: all build linux-amd64 linux-arm64 clean test install

all: linux-amd64 linux-arm64

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) ./cmd/sshfortress/

linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 ./cmd/sshfortress/

linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64 ./cmd/sshfortress/

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./internal/... -v -race -cover

install: build
	cp $(BUILD_DIR)/$(APP_NAME) /usr/local/bin/$(APP_NAME)
	chmod +x /usr/local/bin/$(APP_NAME)
	@echo "Installed $(APP_NAME) to /usr/local/bin/"
