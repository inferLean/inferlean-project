BINARY ?= inferLean
BIN_DIR ?= bin
CMD_PATH ?= ./cmd/infer-lean
LDFLAGS ?= -s -w

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: build build-linux-amd64 build-linux-arm64 swagger clean

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD_PATH)

build-linux-amd64:
	$(MAKE) build GOOS=linux GOARCH=amd64

build-linux-arm64:
	$(MAKE) build GOOS=linux GOARCH=arm64

swagger:
	cd backend && go run github.com/swaggo/swag/cmd/swag@v1.16.4 init -g main.go -o docs --parseDependency --parseInternal

clean:
	rm -rf $(BIN_DIR)
