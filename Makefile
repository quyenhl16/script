BINARY_NAME := syssetup
VERSION ?= 0.1.0
DIST_DIR := dist
PACKAGE_NAME = $(BINARY_NAME)-$(VERSION)-linux-$(ARCH)
STAGING_DIR = $(DIST_DIR)/$(PACKAGE_NAME)
GO_BUILD_FLAGS := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: test build build-linux package package-linux-amd64 package-linux-arm64 package-arch clean

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/syssetup ./cmd/syssetup

build-linux:
	mkdir -p "$(DIST_DIR)"
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/syssetup-linux-amd64 ./cmd/syssetup
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/syssetup-linux-arm64 ./cmd/syssetup

# Build and package both Linux architectures.
package: package-linux-amd64 package-linux-arm64

package-linux-amd64:
	$(MAKE) package-arch ARCH=amd64 VERSION=$(VERSION)

package-linux-arm64:
	$(MAKE) package-arch ARCH=arm64 VERSION=$(VERSION)

# Internal target. Use package-linux-amd64, package-linux-arm64 or package.
package-arch:
	@test -n "$(ARCH)" || (echo "ARCH is required" >&2; exit 1)
	rm -rf "$(STAGING_DIR)"
	mkdir -p "$(STAGING_DIR)/bin"
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o "$(STAGING_DIR)/bin/$(BINARY_NAME)" ./cmd/syssetup
	cp -R features profiles scripts workflows workflow-configs checks "$(STAGING_DIR)/"
	cp README.md setup.sh "$(STAGING_DIR)/"
	chmod +x "$(STAGING_DIR)/bin/$(BINARY_NAME)" "$(STAGING_DIR)/setup.sh"
	find "$(STAGING_DIR)/features" "$(STAGING_DIR)/scripts" -type f -name '*.sh' -exec chmod +x {} +
	tar -C "$(DIST_DIR)" -czf "$(DIST_DIR)/$(PACKAGE_NAME).tar.gz" "$(PACKAGE_NAME)"
	rm -rf "$(STAGING_DIR)"
	@echo "Created $(DIST_DIR)/$(PACKAGE_NAME).tar.gz"

clean:
	rm -rf bin "$(DIST_DIR)"
