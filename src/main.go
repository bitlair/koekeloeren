package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
)

func main() {
	stream, err := ffmpeg("rtsp://0.0.0.0/Streaming/channels/1", "framerate=2, scale=w=800:h=600, boxblur=2:2")
	if err != nil {
		log.Fatal(err)
	}

	i := 0
	for img := range stream {
		i++
		fd, err := os.Create(fmt.Sprintf("img-%d.jpeg", i))
		if err != nil {
			log.Fatal(err)
		}
		jpeg.Encode(fd, img, nil)
		fd.Close()
	}

}

func ffmpeg(source string, video_filters string) (<-chan image.Image, error) {
	cmd := exec.Command(
		"ffmpeg",
		"-i", source,
		"-an",
		"-vf", video_filters,
		"-y",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"-updatefirst", "1",
		"-",
	)
	channel := make(chan image.Image, 0)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
			if len(data) <= 3 {
				return 0, nil, nil
			}
			if !bytes.Equal(data[0:3], []byte{0xff, 0xd8, 0xff}) {
				return 0, nil, fmt.Errorf("Uprocessed data found in stream")
			}
			for i := range data[:len(data)-2] {
				if bytes.Equal(data[i:i+2], []byte{0xff, 0xd9}) {
					return i + 2, data[:i+2], nil
				}
			}
			return 0, nil, nil
		})

		for scanner.Scan() {
			img, err := jpeg.Decode(bytes.NewReader(scanner.Bytes()))
			if err != nil {
				// When FFmpeg has connected it may not have received a
				// keyframe yet, which somehow results in broken frames.
				continue
			}
			select {
			case channel <- img:
			default:
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return channel, nil
}
