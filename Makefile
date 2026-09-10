GO_SRC ?= $(shell find . -name '*.go')

.PHONY: build test test-acc update check lint lint-go format fmt docs generate tidy

build:
	nix build .#

test:
	go tool ginkgo run -r

test-acc:
	TF_ACC=1 go test ./...

update:
	nix flake update

check lint: lint-go
	nix flake check

lint-go:
	golangci-lint run ./...

format fmt:
	nix fmt

docs generate:
	tfplugindocs generate

tidy: go.sum nix/gomod2nix.toml

go.sum: go.mod ${GO_SRC}
	go mod tidy

nix/gomod2nix.toml: go.sum ${GO_SRC}
	gomod2nix generate --dir ${CURDIR} --outdir ${@D}
