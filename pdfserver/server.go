package pdfserver

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"

	"fmt"
)

type Task struct {
	url, id, callback string
}

var config *Config
var Tasks chan Task

type errorHandler func(http.ResponseWriter, *http.Request) error

func (fn errorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := fn(w, r); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// get the first value of param or error
func getParam(params url.Values, name string) (string, error) {
	val := params.Get(name)

	if val == "" {
		return "", fmt.Errorf("Missing param %v", name)
	}

	return val, nil
}

func writeJSONMessage(w http.ResponseWriter, msg interface{}) error {
	blob, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	w.Header()["Content-Type"] = []string{"application/json"}
	w.Write(blob)
	return nil
}

func writeJSONError(w http.ResponseWriter, kind string, err error) error {
	return writeJSONMessage(w, struct {
		Type  string
		Error string
	}{kind, err.Error()})
}

func StartPdfServer(listenTo string, _config *Config) error {
	config = _config
	Tasks = make(chan Task, 1024)

	for i := 0; i < config.NumWorkers; i++ {
		go ConvertWorker(Tasks)
	}

	http.Handle("/convert", errorHandler(convertHandler))

	log.Print("Listening on: " + listenTo)
	return http.ListenAndServe(listenTo, nil)
}
