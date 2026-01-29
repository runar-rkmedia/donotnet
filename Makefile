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

test-e2e:
	DONOTNET_COMPARE=1 go test ./e2e -v -count=1

coverage-e2e: test-e2e
	go tool cover -html=e2e/coverage-e2e.txt -o e2e/coverage-e2e.html
	@echo "Coverage report: e2e/coverage-e2e.html"

clean:
	rm -f $(BINARY_NAME)
	rm -rf dist/
	rm -f e2e/coverage-e2e.txt e2e/coverage-e2e.html

.PHONY: dev test test-e2e coverage-e2e build lint audit release bump tag clean check-clean CHANGELOG.md
