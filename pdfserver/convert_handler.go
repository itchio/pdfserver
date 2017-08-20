package pdfserver

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

func convertHandler(w http.ResponseWriter, r *http.Request) error {
	params := r.URL.Query()

	pdf_url, err := getParam(params, "url")
	if err != nil {
		return err
	}

	id, err := getParam(params, "id")
	if err != nil {
		return err
	}

	_, err = strconv.Atoi(id)
	if err != nil {
		return errors.New("id is not a number")
	}

	callbackURL, err := getParam(params, "callback")
	if err != nil {
		return err
	}

	task := Task{url: pdf_url, id: id, callback: callbackURL}

	select {
	case Tasks <- task:
		return writeJSONMessage(w, struct {
			Processing bool
			Async      bool
		}{true, true})
	case <-time.After(time.Second * 10):
		return writeJSONMessage(w, struct {
			Processing bool
		}{false})
	}

}
