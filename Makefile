.PHONY: build install clean deps lint

BINARY  := kuroko
INSTALL := $(HOME)/.local/bin

build: deps
	go build -o $(BINARY) ./cmd/kuroko

install: build
	mkdir -p $(INSTALL)
	cp $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"
	@echo "Make sure $(INSTALL) is in your PATH"

deps:
	go mod tidy

clean:
	rm -f $(BINARY)

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
