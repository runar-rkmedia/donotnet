BINARY_NAME := donotnet

dev:
	go run .

test:
	go test ./...

build:
	go build -o $(BINARY_NAME) .

lint:
	go tool golangci-lint run --fix

audit:
	go tool govulncheck ./...
	$(MAKE) lint

CHANGELOG.md:
	git cliff -o CHANGELOG.md

tag:
	git tag "$$(git cliff --bumped-version)" -m "$$(git cliff -u --strip all)"

release: bump
	goreleaser release

check-clean:
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Error: working directory is not clean"; \
		git status --short; \
		exit 1; \
	fi

bump: check-clean
	git cliff --bump -o CHANGELOG.md
	git add CHANGELOG.md
	git commit --amend
	$(MAKE) tag

clean:
	rm -f $(BINARY_NAME)
	rm -rf dist/

.PHONY: dev test build lint audit release bump tag clean check-clean CHANGELOG.md
