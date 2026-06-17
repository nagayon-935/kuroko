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
