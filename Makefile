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

.PHONY: vet lint test tidy-check verify vulncheck cover check bench profile

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

# Benchmarks and profiles (docs/PERFORMANCE.md) write under BENCH_DIR,
# which git ignores. BENCH selects benchmarks by regexp, BENCH_COUNT sets
# the runs per benchmark (benchstat needs several to compare) and
# BENCH_FLAGS passes more go test flags, such as -benchtime=100ms.
BENCH_DIR ?= bench
# Module commands run in the module's directory: the path is made absolute,
# relative to this directory.
override BENCH_DIR := $(abspath $(BENCH_DIR))
BENCH ?= .
BENCH_COUNT ?= 6
BENCH_FLAGS ?=

# outname turns the absolute directory in $$dir into a result file name:
# its path in the repository with "/" as "-", "srvr" for the root.
outname = n=$${dir\#$(CURDIR)}; n=$${n\#/}; n=$$(echo "$${n:-srvr}" | tr / -)

# bench writes every module's benchmark results to BENCH_DIR/<module>.txt,
# named by outname (srvr.txt, ginsrvr.txt, internal-paritytest.txt); a
# module directory missing from the tree, such as one a branch adds when
# its base is benchmarked, is skipped.
bench:
	@mkdir -p $(BENCH_DIR)
	@set -e; for m in $(MODULES); do \
		if [ ! -d $$m ]; then echo "== $$m: not in this tree, skipped"; continue; fi; \
		dir=$$(cd $$m && pwd); $(outname); out=$(BENCH_DIR)/$$n.txt; \
		echo "== $$m: bench -> $$out"; \
		if ! (cd $$m && GOWORK=off go test -run '^$$' -bench '$(BENCH)' -benchmem -count=$(BENCH_COUNT) $(BENCH_FLAGS) ./...) > $$out; then \
			cat $$out; exit 1; \
		fi; \
	done

# profile runs the benchmarks of every package that has any once, writing
# a CPU and a memory profile per package, named by outname: go test
# profiles one package at a time. BENCH_DIR/srvr.cpu is the core
# package's, BENCH_DIR/engine.mem, BENCH_DIR/ginsrvr.cpu and so on. Read
# one with `go tool pprof -top BENCH_DIR/<name>.cpu`.
profile:
	@mkdir -p $(BENCH_DIR)
	@set -e; for m in $(MODULES); do \
		for dir in $$(cd $$m && GOWORK=off go list -f '{{.Dir}}' ./...); do \
			grep -qs '^func Benchmark' $$dir/*_test.go || continue; \
			$(outname); \
			echo "== $$dir: profile -> $(BENCH_DIR)/$$n.{cpu,mem}"; \
			if ! (cd $$dir && GOWORK=off go test -run '^$$' -bench '$(BENCH)' -benchmem $(BENCH_FLAGS) \
				-cpuprofile $(BENCH_DIR)/$$n.cpu -memprofile $(BENCH_DIR)/$$n.mem \
				-o $(BENCH_DIR)/$$n.test .) > $(BENCH_DIR)/$$n.profile.txt; then \
				cat $(BENCH_DIR)/$$n.profile.txt; exit 1; \
			fi; \
		done; \
	done
