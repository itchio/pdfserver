package pdfserver

import (
	"encoding/json"
	"io/ioutil"
	"log"
)

var DefaultConfigFname = "pdfserver.json"

type Config struct {
	MaxFileSize int64
	MaxPages    int
	MaxPageSize int64
	TempPath    string
	NumWorkers  int
}

var defaultConfig = Config{
	MaxFileSize: 1024 * 1024 * 250,
	MaxPages:    400,
	MaxPageSize: 1024 * 1024 * 4,
	TempPath:    "tmp",
	NumWorkers:  6,
}

func LoadConfig(fname string) *Config {
	jsonBlob, err := ioutil.ReadFile(fname)

	if err != nil {
		log.Fatal(err)
	}

	config := defaultConfig
	err = json.Unmarshal(jsonBlob, &config)

	if err != nil {
		log.Fatal("Failed parsing config: " + fname + ": " + err.Error())
	}

	return &config
}
