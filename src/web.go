package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
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
	// The duration after which the stream will be closed.
	ViewLimit time.Duration
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
	handler.consumers[id] = ch
	if handler.options.NumViewersCallback != nil {
		if err := handler.options.NumViewersCallback(len(handler.consumers)); err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(res, "%v", err)
			handler.lock.Unlock()
			close(ch)
			return
		}
	}
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

		fmt.Fprintf(res, "Content-Type: image/jpeg\n")
		fmt.Fprintf(res, "Content-Length: %d\n", len(imgBuf))
		fmt.Fprintf(res, "\n")
		if _, err := res.Write(imgBuf); err != nil {
			log.Println(err)
			break
		}
		fmt.Fprintf(res, "%s\n", BOUNDARY)
	}
}
