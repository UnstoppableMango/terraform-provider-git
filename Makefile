GO        ?= go
GOMOD2NIX ?= gomod2nix
GINKGO    ?= ginkgo

GO_SRC ?= $(shell find . -name '*.go')

build:
	nix build .#

test:
	$(GINKGO) run -r

update:
	nix flake update

check lint:
	nix flake check

format fmt:
	nix fmt

tidy: go.sum

go.sum: go.mod ${GO_SRC} nix/gomod2nix.toml
	$(GO) mod tidy

nix/gomod2nix.toml: go.sum ${GO_SRC}
	$(GOMOD2NIX) generate --dir ${CURDIR} --outdir ${@D}
