package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net/http"
	"sync"
)

type StreamHandler struct {
	stream             <-chan image.Image
	consumers          map[uint64]chan<- []byte
	lock               sync.Mutex
	enum               uint64
	numViewersCallback func(int) error
}

type StreamHandlerOptions struct {
	// A function which is called when the number of total stream consumers
	// changes. It may return an error to prevent the stream from being viewed.
	NumViewersCallback func(int) error
}

func NewStreamHandler(stream <-chan image.Image, options *StreamHandlerOptions) *StreamHandler {
	handler := &StreamHandler{
		stream:             stream,
		consumers:          map[uint64]chan<- []byte{},
		numViewersCallback: options.NumViewersCallback,
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
	if handler.numViewersCallback != nil {
		if err := handler.numViewersCallback(len(handler.consumers)); err != nil {
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
		if handler.numViewersCallback != nil {
			// The stream is being closed, any error can be ignored.
			_ = handler.numViewersCallback(len(handler.consumers))
		}
	}()

	res.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+BOUNDARY)
	res.WriteHeader(http.StatusOK)
	for imgBuf := range ch {
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
