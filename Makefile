GO ?= go

# The golangci-lint version CI's lint job runs (.golangci-lint-version,
# read by both this Makefile and the golangci-lint-action `version-file`
# input, so the two can't drift) and where `make lint` caches its
# release binary once downloaded. hack/install-golangci-lint.sh downloads
# the release binary and verifies its checksum, instead of building
# golangci-lint from source with the local Go toolchain. `make clean`
# removes it along with the rest of bin/.
GOLANGCI_LINT_VERSION := $(shell cat .golangci-lint-version)
GOLANGCI_LINT_DIR     := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT         := $(GOLANGCI_LINT_DIR)/golangci-lint

# Every shell or OpenRC script under hack/ and images/, found by its
# shebang, for shellcheck.
SHELL_SCRIPTS := $(shell grep -rIl -e '^\#!.*\(sh\|openrc-run\)$$' hack images 2>/dev/null)

.PHONY: build test vet lint actionlint shellcheck sync-incus-agent clean

build:
	$(GO) build -o bin/buildimg ./cmd/buildimg

test:
	$(GO) vet ./...
	$(GO) test ./...

vet:
	$(GO) vet ./...

$(GOLANGCI_LINT):
	./hack/install-golangci-lint.sh $(GOLANGCI_LINT_VERSION) $(GOLANGCI_LINT_DIR)

lint: $(GOLANGCI_LINT) shellcheck
	$(GOLANGCI_LINT) run ./...
	$(GO) tool actionlint

actionlint:
	$(GO) tool actionlint

shellcheck:
	$(if $(SHELL_SCRIPTS),shellcheck -s sh $(SHELL_SCRIPTS))

# Regenerates the Incus files of images/incus-support from the pinned release.
sync-incus-agent:
	./hack/sync-incus-agent.sh

clean:
	rm -rf bin
