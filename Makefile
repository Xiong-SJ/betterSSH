APP      := betterSSH
PKG      := ./cmd/betterSSH
BIN      := bin/$(APP)
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)
LDFLAGS  := -s -w -X betterSSH/internal/version.Version=$(VERSION)

.PHONY: all build test fmt vet check install clean run

all: build

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

vet:
	go vet ./...

check: fmt vet test build

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 $(BIN) $(DESTDIR)$(PREFIX)/bin/$(APP)

PREFIX ?= /usr/local

run:
	go run $(PKG)

clean:
	rm -rf bin
