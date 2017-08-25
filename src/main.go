package main

import (
	"bytes"
	"encoding/json"
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
	r.HandlerFunc("GET", "/hoofdruimte.jpg", htStreamMjpeg)

	if BUILD == "release" {
		r.NotFound = http.RedirectHandler("/", http.StatusTemporaryRedirect)
	}

	log.Printf("Now accepting HTTP connections on %v", config.Address)
	server := &http.Server{
		Addr:           config.Address,
		Handler:        r,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	log.Fatal(server.ListenAndServe())
}

func htMainPage(http.ResponseWriter, *http.Request) {
}

func htStreamMjpeg(http.ResponseWriter, *http.Request) {
}
