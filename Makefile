pr: *.go
	go build

fmt:
	go fmt *.go

run: pr
	./pr
