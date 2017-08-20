
.PHONY: install test

install:
	go install github.com/itchio/pdfserver

test:
	go test -v github.com/itchio/pdfserver/pdfserver
