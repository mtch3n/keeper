# keeper — build, check, run.
#
# The UI is embedded in the daemon binary, so `build` depends on `ui`. Nothing
# here needs a network after the first `pnpm install` and `go mod download`.

GO      ?= go
PNPM    ?= pnpm
GOFLAGS ?=
BIN     ?= bin
PKGS    := ./...

.PHONY: all build ui ui-audit check fmt vet staticcheck test test-race lint-explain clean tidy dev changelog

all: check build

## build — one binary with the UI embedded, no cgo.
##
## One artefact rather than three: a daemon and a client from different builds
## refuse each other (SPEC §3.4), and shipping one executable is how that stops
## being possible to do by halves. It is also one thing to verify, which matters
## for software whose subject is holding a database credential.
build: ui
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o $(BIN)/keeper ./cmd/keeper

## ui — build the React app and stage it where //go:embed can see it
ui:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build
	rm -rf internal/api/dist
	mkdir -p internal/api/dist
	cp -R web/dist/. internal/api/dist/

ui-audit:
	cd web && $(PNPM) lint && $(PNPM) ui-audit

## check — everything that must pass before work is called done
check: fmt vet staticcheck lint-explain test-race ui-audit

fmt:
	@out=$$(gofmt -l . | grep -v '^web/' || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet $(PKGS)

staticcheck:
	staticcheck $(PKGS)

test:
	$(GO) test $(PKGS)

test-race:
	$(GO) test -race $(PKGS)

## lint-explain — SPEC R7.4b: EXPLAIN ANALYZE is prohibited in the codebase.
## Go cannot make a string literal fail to compile, so this is the enforcement.
lint-explain:
	@sh scripts/lint-explain-analyze.sh

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
	rm -rf $(BIN) internal/api/dist web/dist
