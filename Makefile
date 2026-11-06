# keeper — build, check, run.
#
# The UI is embedded in the daemon binary, so `build` depends on `ui`. Nothing
# here needs a network after the first `pnpm install` and `go mod download`.

GO      ?= go
PNPM    ?= pnpm
GOFLAGS ?=
BIN     ?= bin
PKGS    := ./...
# The version a build reports: the tag, and how far past it, from git. A tree
# with no tags reports dev, which `keeper update` treats as older than every release.
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null | sed 's/^v//' | grep . || echo dev)
LDFLAGS := -X github.com/mtchen/keeper/internal/build.Version=$(VERSION)

.PHONY: all build install ui ui-audit check fmt vet staticcheck test test-race lint-explain plugin-version clean tidy dev changelog

all: check build

## build — one binary with the UI embedded, no cgo.
##
## One artefact rather than three: a daemon and a client from different builds
## refuse each other (SPEC §3.4), and shipping one executable is how that stops
## being possible to do by halves. It is also one thing to verify, which matters
## for software whose subject is holding a database credential.
build: ui
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN)/keeper ./cmd/keeper

## install — build and install bin/keeper in ~/.local/bin, where the plugin's
## launcher looks. The running daemon moves onto it on the next MCP call.
install: build
	install -m 0755 $(BIN)/keeper $(HOME)/.local/bin/keeper

## ui — build the React app and stage it where //go:embed can see it
ui:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build
	find internal/api/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/api/dist/

ui-audit:
	cd web && $(PNPM) lint && $(PNPM) test && $(PNPM) ui-audit

## check — everything that must pass before work is called done
check: fmt vet staticcheck lint-explain plugin-version test-race ui-audit

fmt:
	@out=$$(gofmt -l . | grep -v '^web/' || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet $(PKGS)

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 $(PKGS)

test:
	$(GO) test $(PKGS)

test-race:
	$(GO) test -race $(PKGS)

## lint-explain — SPEC R7.4b: EXPLAIN ANALYZE is prohibited in the codebase.
## Go cannot make a string literal fail to compile, so this is the enforcement.
lint-explain:
	@sh scripts/lint-explain-analyze.sh

## plugin-version — a change under plugin/ needs a version bump (installed plugins are cached by version)
plugin-version:
	@sh scripts/check-plugin-version.sh

## changelog — prepend the commits since the last tag. TAG=v1.2.3 to head them
## under a version. Nothing needs installing; uvx fetches git-cliff.
changelog:
	scripts/changelog.sh $(TAG)

tidy:
	$(GO) mod tidy

## dev — daemon with the UI served from Vite instead of the embedded bundle
dev:
	$(GO) run ./cmd/keeper daemon serve --dev

clean:
	rm -rf $(BIN) web/dist
	find internal/api/dist -mindepth 1 ! -name .gitkeep -delete
