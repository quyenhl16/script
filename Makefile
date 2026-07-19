.PHONY: test build build-linux clean

test:
	go test ./...

build:
	go build -o bin/syssetup ./cmd/syssetup

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/syssetup-linux-amd64 ./cmd/syssetup
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/syssetup-linux-arm64 ./cmd/syssetup

clean:
	rm -rf bin dist
