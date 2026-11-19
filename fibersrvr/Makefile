# Targets run for this module through the root Makefile, which runs each
# with GOWORK=off from this directory; see ../Makefile.
.PHONY: vet lint test tidy-check verify vulncheck check bench profile

vet lint test tidy-check verify vulncheck check bench profile:
	$(MAKE) -C .. $@ MODULES=$(notdir $(CURDIR))
