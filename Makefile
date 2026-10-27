GO        ?= go
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
MODULE    := $(shell $(GO) list -m 2>/dev/null)
LDFLAGS   := -s -w -X $(MODULE)/internal/server.Version=$(VERSION)
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test install dist relay-image

build:                      ## both binaries into bin/
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

test:
	$(GO) vet ./... && $(GO) test ./...

install:                    ## ask-form into $(GOBIN) or ~/go/bin
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/ask-form

dist:                       ## release binaries for $(PLATFORMS) + checksums.txt into dist/
	rm -rf dist && mkdir -p dist
	for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
	  for b in ask-form ask-form-relay; do \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	      -o dist/$$b-$$os-$$arch$$ext ./cmd/$$b || exit 1; \
	  done; \
	done
	cd dist && sha256sum ask-form* > checksums.txt

relay-image:                ## the relay's container image
	docker build --build-arg VERSION=$(VERSION) -t ask-form-relay:local .
