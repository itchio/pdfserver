package main

import (
	"flag"
	"log"

	. "github.com/itchio/pdfserver/pdfserver"
)

var (
	configFname string
	listenTo    string
)

func init() {
	flag.StringVar(&configFname, "config", DefaultConfigFname, "Path to json config file")
	flag.StringVar(&listenTo, "listen", "127.0.0.1:8091", "Address to listen to")
}

func main() {
	flag.Parse()
	config := LoadConfig(configFname)

	if err := StartPdfServer(listenTo, config); err != nil {
		log.Fatal(err)
	}
}
