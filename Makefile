BINARY := tomcat-sentinel
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test vet build clean dist-linux package-linux

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/tomcat-sentinel

dist-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/tomcat-sentinel
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/tomcat-sentinel

package-linux: dist-linux
	rm -rf dist/package
	rm -f dist/*.tar.gz dist/SHA256SUMS
	mkdir -p dist/package
	for arch in amd64 arm64; do \
		name="$(BINARY)-$(VERSION)-linux-$$arch"; \
		mkdir -p "dist/package/$$name"; \
		cp "dist/$(BINARY)-linux-$$arch" "dist/package/$$name/$(BINARY)"; \
		cp LICENSE README.md "dist/package/$$name/"; \
		COPYFILE_DISABLE=1 tar -C dist/package -czf "dist/$$name.tar.gz" "$$name"; \
	done
	cd dist && { command -v sha256sum >/dev/null && sha256sum *.tar.gz || shasum -a 256 *.tar.gz; } > SHA256SUMS

clean:
	rm -rf bin dist
