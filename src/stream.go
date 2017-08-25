package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os/exec"
)

func FFmpegStream(source string, videoFilters string) (<-chan image.Image, error) {
	cmd := exec.Command(
		"ffmpeg",
		"-i", source,
		"-an",
		"-vf", videoFilters,
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
		if err := scanner.Err(); err != nil {
			log.Printf("Scanner error: %v", err)
		}
		log.Println("FFmpeg exited")
	}()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return channel, nil
}
