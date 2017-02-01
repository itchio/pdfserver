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

func ConvertWorker (tasks chan Task) () {
	process := func(task Task) (int, error) {
		pdf_url := task.url
		id := task.id

		os.MkdirAll(config.TempPath + "/" + id, 0700)

		file, err := os.Create(config.TempPath + "/" + id + "/pdf.pdf")
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

		_, err = io.CopyN(ioutil.Discard, res.Body, 1)
		if err != io.EOF {
			log.Print("File was too big")
			return 0, errors.New("File was too big")
		}

		file.Close()

		pdf, err := pdf.Open(config.TempPath + "/" + id + "/pdf.pdf")

		if err != nil {
			log.Print("Failed to load PDF " + id + ": " + err.Error())
			return 0, err
		}

		pages := pdf.NumPage()

		if pages > config.MaxPages {
			return pages, fmt.Errorf("PDF has too many pages (%d)", pages)
		}

		cmd := exec.Command("pdf2svg", config.TempPath + "/" + id + "/pdf.pdf", config.TempPath + "/" + id + "/page%d.svg", "all")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()

		for page := 0; page < pages; page++ {
			if _, err := os.Stat(fmt.Sprintf(config.TempPath + "/%s/page%d.svg", id, page + 1)); os.IsNotExist(err) {
				return pages, fmt.Errorf("Page %d failed to convert", page + 1)
			}
		}

		return pages, nil
	}

	for task := range tasks {
		func(task Task) {
			pages, err := process(task)

			resValues := url.Values{}
			success := err == nil

			resValues.Add("ID", task.id)
			if err != nil {
				resValues.Add("Success", "false")
				resValues.Add("Error", err.Error())
			} else {
				resValues.Add("Success", "true")
				resValues.Add("Pages", strconv.Itoa(pages))
			}

			defer os.RemoveAll(config.TempPath + "/" + task.id)

			res, err := http.PostForm(task.callback, resValues)
			if err != nil {
				log.Print("Failed to deliver callback: " + err.Error())
				return
			}

			defer res.Body.Close()

			if !success {
				return
			}

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

			done := make(chan bool)

			// todo: probably shouldn't create this many goroutines?
			for page := 0; page < pages; page++ {
				go (func(page int) {
					uploadDone := make(chan bool)
					uploadErrs := make(chan error)

					svg, err := os.Open(fmt.Sprintf(config.TempPath + "/%s/page%d.svg", task.id, page + 1))
					if err != nil {
						log.Printf("Failed to open page %d: %s", page + 1, err.Error())
						done <- false
						return
					}

					stateConsumer := &state.Consumer{}

					writer, err := uploader.NewResumableUpload(response.UploadURLs[page],
						uploadDone, uploadErrs, uploader.ResumableUploadSettings{
							Consumer: stateConsumer,
						})

					io.Copy(writer, svg)

					writer.Close()

					select {
						case err := <-uploadErrs:
							log.Printf("Page %d (PDF %s) failed to upload: %s", page, id, err.Error())
							done <- false
						case <-uploadDone:
							done <- true
					}
				})(page)
			}

			// wait for all pages to finish uploading before removing the directory
			for page := 0; page < pages; page++ {
				<-done
			}

		}(task)
	}
}
