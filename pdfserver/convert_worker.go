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
	"strings"

	"github.com/itchio/httpkit/uploader"
	"github.com/itchio/wharf/state"
	"launchpad.net/xmlpath"
	"rsc.io/pdf"
)

type ConversionFinishedResponse struct {
	UploadURLs []string `json:"upload_urls"`
}

type PdfConversionResult struct {
	Pages       int
	PageFormats []string
}

func ConvertWorker (tasks chan Task) () {
	process := func(task Task) (*PdfConversionResult, error) {
		pdf_url := task.url
		id := task.id

		os.MkdirAll(config.TempPath + "/" + id, 0700)

		file, err := os.Create(config.TempPath + "/" + id + "/pdf.pdf")
		if err != nil {
			return nil, err
		}

		log.Print("Fetching URL: ", pdf_url)
		client := http.Client{}
		res, err := client.Get(pdf_url)

		if err != nil {
			return nil, err
		}

		defer res.Body.Close()

		if res.StatusCode != 200 {
			return nil, fmt.Errorf("Failed to fetch file: %d", res.StatusCode)
		}

		_, err = io.CopyN(file, res.Body, config.MaxFileSize)
		if err != nil && err != io.EOF {
			return nil, err
		}

		log.Print("Download finished")

		_, err = io.CopyN(ioutil.Discard, res.Body, 1)
		if err != io.EOF {
			log.Print("File was too big")
			return nil, errors.New("File was too big")
		}

		file.Close()

		pdf, err := pdf.Open(config.TempPath + "/" + id + "/pdf.pdf")

		if err != nil {
			log.Print("Failed to load PDF " + id + ": " + err.Error())
			return nil, err
		}

		pages := pdf.NumPage()

		if pages > config.MaxPages {
			return nil, fmt.Errorf("PDF has too many pages (%d)", pages)
		}

		log.Print("Converting...")

		cmd := exec.Command("pdf2svg", config.TempPath + "/" + id + "/pdf.pdf", config.TempPath + "/" + id + "/page%d.svg", "all")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()

		pageFormats := make([]string, pages)

		for page := 1; page < pages + 1; page++ {
			pagePath := fmt.Sprintf(config.TempPath + "/%s/page%d.svg", id, page)
			if _, err := os.Stat(pagePath); os.IsNotExist(err) {
				return nil, fmt.Errorf("Page %d failed to convert", page)
			}

			pageReader, err := os.Open(pagePath)
			defer pageReader.Close()

			root, err := xmlpath.Parse(pageReader)
			if err != nil {
				log.Printf("Failed to parse SVG for page %d: %s", page, err.Error())
			} else {
				// a SVG is considered to be formed of only raster images if it doesn't contain any of:
				// todo: path elements can appear inside clipPaths; don't count those towards visible vector elements

				vectorElements := []string {"circle", "ellipse", "line", "mpath", "path", "polygon",
				  "polyline", "rect", "text"}

				convertToRaster := true

				for _, elem := range vectorElements {
					path := xmlpath.MustCompile("//" + elem)
					if path.Exists(root) {
						convertToRaster = false
						break
					}
				}

				if convertToRaster {
					pageFormats[page - 1] = "jpg"

					rasterPath := fmt.Sprintf(config.TempPath + "/%s/page%d." + pageFormats[page - 1], id, page)
					log.Printf("Page %d only has images; converting to raster", page)

					// TODO: maybe figure out what density/width to use based on the width of the biggest image

					cmd = exec.Command("convert", "-density", "80", pagePath, rasterPath)
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					cmd.Run()

					if _, err = os.Stat(rasterPath); os.IsNotExist(err) {
						log.Printf("Rasterizing page %d failed, uploading SVG", id)
						pageFormats[page - 1] = "svg"
					}
				} else {
					pageFormats[page - 1] = "svg"
				}
			}
		}

		return &PdfConversionResult {
			Pages: pages,
			PageFormats: pageFormats,
		}, nil
	}

	for task := range tasks {
		func(task Task) {
			result, err := process(task)

			resValues := url.Values{}
			success := err == nil

			resValues.Add("ID", task.id)
			if err != nil {
				resValues.Add("Success", "false")
				resValues.Add("Error", err.Error())
			} else {
				resValues.Add("Success", "true")
				resValues.Add("Pages", strconv.Itoa(result.Pages))
				resValues.Add("PageFormats", strings.Join(result.PageFormats, ","))
			}

			// defer os.RemoveAll(config.TempPath + "/" + task.id)

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

			if len(response.UploadURLs) != result.Pages {
				log.Print("Got an invalid amount of upload URLs")
				return
			}

			done := make(chan bool)

			// todo: probably shouldn't create this many goroutines?
			for page := 0; page < result.Pages; page++ {
				go (func(page int) {
					uploadDone := make(chan bool)
					uploadErrs := make(chan error)

					fullPath := fmt.Sprintf(config.TempPath + "/%s/page%d.%s", task.id, page + 1, result.PageFormats[page])
					svg, err := os.Open(fullPath)

					log.Printf("Uploading file %s", fullPath)

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
							log.Printf("Page %d (PDF %s) failed to upload: %s", page, task.id, err.Error())
							done <- false
						case <-uploadDone:
							done <- true
					}
				})(page)
			}

			// wait for all pages to finish uploading before removing the directory

			allDone := true

			for page := 0; page < result.Pages; page++ {
				pageDone := <-done
				allDone = allDone && pageDone
			}

			resValues = url.Values{}

			resValues.Add("ID", task.id)
			if allDone {
				resValues.Add("Success", "true")
				resValues.Add("Uploaded", "true")
			} else {
				resValues.Add("Success", "false")
				resValues.Add("Uploaded", "false")
			}

			res, err = http.PostForm(task.callback, resValues)
			if err != nil {
				log.Print("Failed to deliver post-upload callback: " + err.Error())
				return
			}

			log.Print("All done!")

			res.Body.Close()

		}(task)
	}
}
