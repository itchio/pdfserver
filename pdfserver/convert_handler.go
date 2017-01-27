package pdfserver

import (
	"errors"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	"github.com/itchio/httpkit/uploader"
	"github.com/itchio/wharf/state"
	"rsc.io/pdf"
)

type ConversionFinishedResponse struct {
	UploadURLs []string `json:"upload_urls"`
}

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

	process := func() (int, error) {
		os.MkdirAll("tmp/" + id, 0700)

		file, err := os.Create("tmp/" + id + "/pdf.pdf")
		if err != nil {
			return 0, err
		}

		log.Print("Fetching URL: ", pdf_url)
		client := http.Client{}
		res, err := client.Get(pdf_url)

		if err != nil {
			return 0, err
		}

		defer res.Body.Close()

		if res.StatusCode != 200 {
			return 0, fmt.Errorf("Failed to fetch file: %d", res.StatusCode)
		}

		_, err = io.CopyN(file, res.Body, config.MaxFileSize)
		if err != nil && err != io.EOF {
			return 0, err
		}

		file.Close()

		log.Print("Done downloading.")

		pdf, err := pdf.Open("tmp/" + id + "/pdf.pdf")
		if err != nil {
			return 0, err
		}

		pages := pdf.NumPage()

		if pages > config.MaxPages {
			return pages, fmt.Errorf("PDF has too many pages (%d)", pages)
		}

		cmd := exec.Command("pdf2svg", "tmp/" + id + "/pdf.pdf", "tmp/" + id + "/page%d.svg", "all")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()

		for page := 0; page < pages; page++ {
			if _, err := os.Stat(fmt.Sprintf("tmp/%s/page%d.svg", id, page + 1)); os.IsNotExist(err) {
				return pages, fmt.Errorf("Page %d failed to convert", page + 1)
			}
		}

		return pages, nil
	}

	go (func() {
		pages, err := process()

		resValues := url.Values{}
		if err != nil {
			resValues.Add("Success", "false")
			resValues.Add("Error", err.Error())
		} else {
			resValues.Add("Success", "true")
			resValues.Add("ID", id)
			resValues.Add("Pages", strconv.Itoa(pages))

			// defer os.RemoveAll("tmp/" + id)
		}

		res, err := http.PostForm(callbackURL, resValues)
		if err != nil {
			log.Print("Failed to deliver callback: " + err.Error())
			return
		}

		defer res.Body.Close()

		data, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Print("Failed to read response: " + err.Error())
			return
		}

		var response ConversionFinishedResponse
		err = json.Unmarshal(data, &response)
		if err != nil {
			log.Print("Failed to parse response: " + err.Error())
			return
		}

		if len(response.UploadURLs) != pages {
			log.Print("Got an invalid amount of upload URLs")
			return
		}

		// todo: probably shouldn't create this many goroutines?
		for page := 0; page < pages; page++ {
			go (func(page int) {
				uploadDone := make(chan bool)
				uploadErrs := make(chan error)

				svg, err := os.Open(fmt.Sprintf("tmp/%s/page%d.svg", id, page + 1))
				if err != nil {
					log.Printf("Failed to open page %d: %s", page + 1, err.Error())
					return;
				}

				stateConsumer := &state.Consumer{
				}

				writer, err := uploader.NewResumableUpload(response.UploadURLs[page],
					uploadDone, uploadErrs, uploader.ResumableUploadSettings{
						Consumer: stateConsumer,
					})

				io.Copy(writer, svg)

				closeErr := writer.Close()
				if closeErr != nil {
					log.Print("Error closing resumable upload: " + closeErr.Error())
				} else {
					log.Printf("Page %d uploaded!", page)
				}
			})(page)
		}
	})()

	return writeJSONMessage(w, struct {
		Processing bool
		Async      bool
	}{true, true})
}
