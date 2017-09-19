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

type FFmpegStream struct {
	Stream chan image.Image

	source       string
	videoFilters string
}

func NewFFmpegStream(source string, videoFilters string) (*FFmpegStream, error) {
	ff := &FFmpegStream{
		Stream:       make(chan image.Image, 0),
		source:       source,
		videoFilters: videoFilters,
	}
	if err := ff.open(); err != nil {
		return nil, err
	}
	return ff, nil
}

func (ff *FFmpegStream) open() error {
	cmd := exec.Command(
		"ffmpeg",
		"-i", ff.source,
		"-q:v", "1",
		"-an",
		"-vf", ff.videoFilters,
		"-y",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"-updatefirst", "1",
		"-",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer([]byte{}, 1<<22) // 4MiB
		scanner.Split(splitJpegs)

		for scanner.Scan() {
			img, err := jpeg.Decode(bytes.NewReader(scanner.Bytes()))
			if err != nil {
				// When FFmpeg has connected it may not have received a
				// keyframe yet, which somehow results in broken frames.
				continue
			}

			select {
			case ff.Stream <- img:
			default:
				// No one seems to be interested in our stream. Pause it by
				// killing FFmpeg and resume when the next send succeeds.
				go func() {
					ff.Stream <- img
					ff.open()
				}()
				cmd.Process.Kill()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Scanner error: %v", err)
		}
		log.Println("FFmpeg exited")
		ff.open() // Reconnect.
	}()
	return cmd.Start()
}

// A splitter function for a bufio.Scanner that splits a stream of multiple
// concatenated JPEG images.
func splitJpegs(data []byte, atEOF bool) (advance int, token []byte, err error) {
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
}
