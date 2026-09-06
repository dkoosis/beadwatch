.PHONY: check install

check:
	go vet ./...
	go test ./...

install:
	go install ./cmd/beadwatch
