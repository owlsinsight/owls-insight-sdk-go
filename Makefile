# The Go tooling needs no Makefile; this only installs the WebSocket conformance
# server's socket.io (pinned by conformance/package-lock.json) before the tests.

.PHONY: test conformance

# `npm ci` in conformance/, once per change of the lockfile.
conformance: conformance/node_modules/.package-lock.json

conformance/node_modules/.package-lock.json: conformance/package.json conformance/package-lock.json
	npm ci --prefix conformance --no-audit --no-fund

# Every test, with the WebSocket conformance tests required instead of skipped.
test: conformance
	OWLS_REQUIRE_WS_SERVER_TESTS=1 go test ./...
