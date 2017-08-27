package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/yosssi/gmq/mqtt"
	"github.com/yosssi/gmq/mqtt/client"

	assets "./assets-go"
)

var (
	PUBLIC  = "public"
	BUILD   = strings.Trim(string(assets.MustAsset("_BUILD")), "\n ")
	VERSION = strings.Trim(string(assets.MustAsset("_VERSION")), "\n ")
)

type Config struct {
	Address string
	URLRoot string

	ViewLimitSeconds int
	NumViewersTopic  string
	AfterLimitImage  string
	FFmpegSource     string
	FFmpegFilters    string

	MQTTDeny []struct {
		Topic string
		Value string
		Image string
	}
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

	stream, err := FFmpegStream(config.FFmpegSource, config.FFmpegFilters)
	if err != nil {
		log.Println(err)
		return
	}

	mqttc := client.New(&client.Options{
		ErrorHandler: func(err error) {
			log.Println(err)
		},
	})
	if err := mqttc.Connect(&client.ConnectOptions{
		Network:  "tcp",
		Address:  "mqtt.bitlair.nl:1883",
		ClientID: []byte("koekeloeren"),
	}); err != nil {
		log.Fatal(err)
	}

	var denyImage image.Image
	denyBits := make([]bool, len(config.MQTTDeny))
	subReqs := make([]*client.SubReq, len(config.MQTTDeny))
	for i, entry := range config.MQTTDeny {
		func(i int, topic, value string) {
			var img image.Image
			if entry.Image != "" {
				fd, err := os.Open(entry.Image)
				if err != nil {
					log.Println(err)
				} else {
					defer fd.Close()
					i, _, err := image.Decode(fd)
					if err != nil {
						log.Println(err)
					} else {
						img = i
					}
				}
			}
			subReqs[i] = &client.SubReq{
				TopicFilter: []byte(topic),
				QoS:         mqtt.QoS1,
				Handler: func(topicName, message []byte) {
					denyBits[i] = string(message) == value
					if denyBits[i] && img != nil {
						denyImage = img
					}
				},
			}
			log.Printf("Added MQTT deny rule: %s == %q", topic, value)
		}(i, entry.Topic, entry.Value)
	}
	if len(subReqs) > 0 {
		if err := mqttc.Subscribe(&client.SubscribeOptions{
			SubReqs: subReqs,
		}); err != nil {
			log.Fatal(err)
		}
	}

	numViewersCallback := func(num int) error {
		return mqttc.Publish(&client.PublishOptions{
			QoS:       mqtt.QoS1, // Setting QoS1 ensures that the message wil reach the broker.
			Retain:    true,
			TopicName: []byte(config.NumViewersTopic),
			Message:   []byte(fmt.Sprintf("%d", num)),
		})
	}
	viewingAllowedCallback := func() (bool, image.Image) {
		denied := false
		for _, b := range denyBits {
			denied = denied || b
		}
		return !denied, denyImage
	}

	antiIndexer := NewAntiIndexer()

	r.Handler("GET", "/", http.RedirectHandler("/hoofdruimte", http.StatusFound))
	r.HandlerFunc("GET", "/hoofdruimte", htMainPage(antiIndexer))
	r.Handler("GET", "/hoofdruimte.mjpg", antiIndexer.Protect(NewStreamHandler(stream, &StreamHandlerOptions{
		NumViewersCallback:     numViewersCallback,
		ViewingAllowedCallback: viewingAllowedCallback,
		ViewLimit:              time.Second * time.Duration(config.ViewLimitSeconds),
		AfterLimit: func() image.Image {
			fd, err := os.Open(config.AfterLimitImage)
			if err != nil {
				log.Println(err)
				return nil
			}
			defer fd.Close()
			img, _, err := image.Decode(fd)
			if err != nil {
				log.Println(err)
				return nil
			}
			return img
		}(),
	})))
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

func htMainPage(antiIndexer *AntiIndexer) func(res http.ResponseWriter, req *http.Request) {
	return func(res http.ResponseWriter, req *http.Request) {
		tmpl := template.Must(template.New("main").Parse(string(assets.MustAsset("view/main.html"))))
		if err := tmpl.Execute(res, map[string]interface{}{
			"token": antiIndexer.Token(req),
		}); err != nil {
			log.Println(err)
		}
	}
}
