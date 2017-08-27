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
	stream    <-chan image.Image
	consumers map[uint64]chan<- []byte
	lock      sync.Mutex
	enum      uint64
}

func NewStreamHandler(stream <-chan image.Image) *StreamHandler {
	handler := &StreamHandler{
		stream:    stream,
		consumers: map[uint64]chan<- []byte{},
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
	res.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+BOUNDARY)
	res.WriteHeader(http.StatusOK)

	id, ch := func() (uint64, <-chan []byte) {
		handler.lock.Lock()
		defer handler.lock.Unlock()
		ch := make(chan []byte, 1)
		handler.enum++
		id := handler.enum
		handler.consumers[id] = ch
		return id, ch
	}()
	defer func() {
		handler.lock.Lock()
		defer handler.lock.Unlock()
		close(handler.consumers[id])
		delete(handler.consumers, id)
	}()

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
