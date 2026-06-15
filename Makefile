BINARY   := tomp4
GOFLAGS  := -ldflags="-s -w"
SRC      := .

.PHONY: all build clean test lint run

all: build

build:
	go build $(GOFLAGS) -o $(BINARY) $(SRC)

clean:
	rm -f $(BINARY)
	go clean

test:
	go test -v ./...

lint:
	go vet ./...

run: build
	./$(BINARY) $(ARGS)

fmt:
	go fmt ./...
