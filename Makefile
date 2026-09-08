BINARY   := tomp4
GOFLAGS  := -ldflags="-s -w"
SRC      := .
PREFIX   ?= /usr/local
BINDIR   := $(PREFIX)/bin

.PHONY: all build clean test lint run install

all: build

build:
	go build $(GOFLAGS) -o $(BINARY) $(SRC)

clean:
	rm -f $(BINARY)
	go clean

install: build
	sudo install -m 0755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

test:
	go test -v ./...

lint:
	go vet ./...

run: build
	./$(BINARY) $(ARGS)

fmt:
	go fmt ./...
