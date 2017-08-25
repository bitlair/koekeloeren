package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"image/jpeg"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	assets "./assets-go"
	"github.com/julienschmidt/httprouter"
)

var (
	PUBLIC  = "public"
	BUILD   = strings.Trim(string(assets.MustAsset("_BUILD")), "\n ")
	VERSION = strings.Trim(string(assets.MustAsset("_VERSION")), "\n ")
)

type Config struct {
	Address string
	URLRoot string

	FFmpegSource  string
	FFmpegFilters string
}

type AssetServeHandler struct {
	name string
}

func (h *AssetServeHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(h.name)))
	http.ServeContent(w, req, h.name, time.Now(), bytes.NewReader(assets.MustAsset(h.name)))
}

func main() {
	confFile := "config.json"
	if len(os.Args) > 1 {
		confFile = os.Args[1]
	}
	log.Printf("Using config file %q", confFile)
	var config Config
	if in, err := os.Open(confFile); err != nil {
		log.Fatal(err)
	} else if err := json.NewDecoder(in).Decode(&config); err != nil {
		log.Fatal(err)
	}

	r := httprouter.New()
	static := map[string][]string{
		"js":  []string{},
		"css": []string{},
	}

	for _, file := range assets.AssetNames() {
		if !strings.HasPrefix(file, PUBLIC) {
			continue
		}
		urlPath := strings.TrimPrefix(file, PUBLIC)
		r.Handler("GET", urlPath, &AssetServeHandler{name: file})

		switch path.Ext(file) {
		case ".css":
			static["css"] = append(static["css"], urlPath)
		case ".js":
			static["js"] = append(static["js"], urlPath)
		}
	}
	for _, a := range static {
		sort.Strings(a)
	}

	r.Handler("GET", "/", http.RedirectHandler("/hoofdruimte", http.StatusFound))
	r.HandlerFunc("GET", "/hoofdruimte", htMainPage)
	r.HandlerFunc("GET", "/hoofdruimte.mjpg", htStreamMjpeg(&config))

	if BUILD == "release" {
		r.NotFound = http.RedirectHandler("/", http.StatusTemporaryRedirect)
	}

	log.Printf("Now accepting HTTP connections on %v", config.Address)
	server := &http.Server{
		Addr:           config.Address,
		Handler:        r,
		ReadTimeout:    time.Hour,
		WriteTimeout:   time.Hour,
		MaxHeaderBytes: 1 << 20,
	}
	log.Fatal(server.ListenAndServe())
}

func htMainPage(res http.ResponseWriter, req *http.Request) {
	tmpl := template.Must(template.New("main").Parse(string(assets.MustAsset("view/main.html"))))
	if err := tmpl.Execute(res, map[string]interface{}{}); err != nil {
		log.Println(err)
	}
}

func htStreamMjpeg(config *Config) func(res http.ResponseWriter, req *http.Request) {
	return func(res http.ResponseWriter, req *http.Request) {
		const BOUNDARY = "--jpegBoundary"
		res.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+BOUNDARY)
		res.WriteHeader(http.StatusOK)

		stream, err := FFmpegStream(config.FFmpegSource, config.FFmpegFilters)
		if err != nil {
			log.Println(err)
			return
		}

		var buf bytes.Buffer
		for img := range stream {
			buf.Reset()
			jpeg.Encode(&buf, img, nil)
			res.Write([]byte(BOUNDARY))
			res.Write([]byte("Content-Type: image/jpeg\n"))
			res.Write([]byte(fmt.Sprintf("Content-Length: %d\n\n", buf.Len())))
			if _, err := io.Copy(res, bytes.NewReader(buf.Bytes())); err != nil {
				log.Println(err)
				return
			}
		}
	}
}
