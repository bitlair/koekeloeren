package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"sync"
	"time"
)

type StreamHandler struct {
	stream    <-chan image.Image
	consumers map[uint64]chan<- []byte
	lock      sync.Mutex
	enum      uint64
	options   *StreamHandlerOptions
}

type StreamHandlerOptions struct {
	// A function which is called when the number of total stream consumers
	// changes. It may return an error to prevent the stream from being viewed.
	NumViewersCallback func(int) error
	// A function called to check whether playback of the stream is allowed.
	ViewingAllowedCallback func() (bool, image.Image)
	// The duration after which the stream will be closed.
	ViewLimit time.Duration
	// An image to display after the view limit has been reached.
	AfterLimit image.Image
}

func NewStreamHandler(stream <-chan image.Image, options *StreamHandlerOptions) *StreamHandler {
	if options == nil {
		options = &StreamHandlerOptions{}
	}
	handler := &StreamHandler{
		stream:    stream,
		consumers: map[uint64]chan<- []byte{},
		options:   options,
	}
	go func() {
		for img := range stream {
			var buf bytes.Buffer
			jpeg.Encode(&buf, img, nil)
			handler.lock.Lock()
			for _, ch := range handler.consumers {
				select {
				case ch <- buf.Bytes():
				default:
				}
			}
			handler.lock.Unlock()
		}
	}()
	return handler
}

func (handler *StreamHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	const BOUNDARY = "--jpegBoundary"

	res.Header().Set("X-Accel-Buffering", "no")

	handler.lock.Lock()
	handler.enum++
	id := handler.enum
	ch := make(chan []byte, 1)
	if handler.options.NumViewersCallback != nil {
		if err := handler.options.NumViewersCallback(len(handler.consumers)); err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(res, "%v", err)
			close(ch)
			handler.lock.Unlock()
			return
		}
	}
	handler.consumers[id] = ch
	handler.lock.Unlock()

	defer func() {
		handler.lock.Lock()
		defer handler.lock.Unlock()
		close(handler.consumers[id])
		delete(handler.consumers, id)
		if handler.options.NumViewersCallback != nil {
			// The stream is being closed, any error can be ignored.
			_ = handler.options.NumViewersCallback(len(handler.consumers))
		}
	}()

	res.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+BOUNDARY)
	res.WriteHeader(http.StatusOK)
	viewStart := time.Now()
	for imgBuf := range ch {
		if handler.options.ViewLimit != 0 && time.Since(viewStart) >= handler.options.ViewLimit {
			break
		}
		if cb := handler.options.ViewingAllowedCallback; cb != nil {
			allowed, img := cb()
			if !allowed {
				var imgBuf bytes.Buffer
				jpeg.Encode(&imgBuf, img, &jpeg.Options{Quality: 100})
				fmt.Fprintf(res, "Content-Type: image/jpeg\n")
				fmt.Fprintf(res, "Content-Length: %d\n", imgBuf.Len())
				fmt.Fprintf(res, "\n")
				res.Write(imgBuf.Bytes())
				fmt.Fprintf(res, "%s\n", BOUNDARY)
				continue
			}
		}

		fmt.Fprintf(res, "Content-Type: image/jpeg\n")
		fmt.Fprintf(res, "Content-Length: %d\n", len(imgBuf))
		fmt.Fprintf(res, "\n")
		if _, err := res.Write(imgBuf); err != nil {
			return
		}
		fmt.Fprintf(res, "%s\n", BOUNDARY)
	}

	if handler.options.AfterLimit != nil {
		var imgBuf bytes.Buffer
		jpeg.Encode(&imgBuf, handler.options.AfterLimit, &jpeg.Options{Quality: 100})
		fmt.Fprintf(res, "Content-Type: image/jpeg\n")
		fmt.Fprintf(res, "Content-Length: %d\n", imgBuf.Len())
		fmt.Fprintf(res, "\n")
		res.Write(imgBuf.Bytes())
		fmt.Fprintf(res, "%s\n", BOUNDARY)
	}
}

// Middleware for protection against indexing webspiders.
//
// Protection works by requiring a hash based upon the current date and a salt
// to be present in the stream request. This causes any links indexed to stop
// working after midnight.
type AntiIndexer struct {
	Denied http.Handler

	salt []byte
}

func NewAntiIndexer() *AntiIndexer {
	ai := &AntiIndexer{
		salt: make([]byte, 32),
	}
	rand.Read(ai.salt)
	return ai
}

func (ai *AntiIndexer) Protect(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		if req.FormValue("token") == ai.Token() {
			handler.ServeHTTP(res, req)
		} else if ai.Denied != nil {
			ai.Denied.ServeHTTP(res, req)
		} else {
			http.Error(res, "Invalid token", http.StatusUnauthorized)
		}
	})
}

func (ai *AntiIndexer) Token() string {
	hash := sha512.New()
	hash.Write([]byte(time.Now().Format("2006-01-02")))
	hash.Write(ai.salt)
	return base64.RawURLEncoding.EncodeToString(hash.Sum([]byte{}))
}
