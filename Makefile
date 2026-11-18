# Every target loops over MODULES and runs in each module's directory with
# GOWORK=off, as released modules are built by their consumers. Run one
# module with, for example, `make test MODULES=ginsrvr`; each module's own
# Makefile does that for its directory.
MODULES ?= . ginsrvr fibersrvr otelsrvr otelsetup internal/paritytest

GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@v1.9.0
GOLANGCI_LINT ?= golangci-lint

# each runs $(1) in every module, stopping at the first failure.
define each
	@set -e; for m in $(MODULES); do echo "== $$m: $(1)"; (cd $$m && GOWORK=off $(1)); done
endef

.PHONY: vet lint test tidy-check verify vulncheck cover check

vet:
	$(call each,go vet ./...)

lint:
	$(call each,$(GOLANGCI_LINT) run ./...)

test:
	$(call each,go test -race ./...)

tidy-check:
	$(call each,go mod tidy -diff)

verify:
	$(call each,go mod verify)

vulncheck:
	$(call each,$(GOVULNCHECK) ./...)

# cover runs every module's tests in workspace mode (go.work), so the engine
# packages count the coverage the engine modules' tests give them, and
# prints each function and the engine package's total.
cover:
	go test -coverpkg=github.com/kartaladev/srvr/... -coverprofile=c.out github.com/kartaladev/srvr/...
	go tool cover -func=c.out
	@awk -F'[: ,]' 'NR > 1 && $$1 ~ /\/srvr\/engine\// { key = $$1 ":" $$2 "," $$3; n[key] = $$4; if ($$5 > 0) hit[key] = 1 } \
		END { for (k in n) { total += n[k]; if (k in hit) covered += n[k] }; printf "engine: %.1f%% of statements\n", 100 * covered / total }' c.out

check: vet lint test tidy-check verify vulncheck
