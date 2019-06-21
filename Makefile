distriproxy:
	# for a statically linked binary we need to disable cgo
	CGO_ENABLED=0 go build

.PHONY: clean

clean:
	go clean
