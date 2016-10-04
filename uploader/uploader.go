package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const clipTimeFmt = "20060102150405"

var (
	cleanup  = flag.Bool("cleanup", false, "remove stuff when done")
	camid    = flag.String("camid", "", "Camera ID")
	campass  = flag.String("campass", "", "Camera Password")
	endpoint = flag.String("endpoint", "", "Where to send the bits")
)

type clip struct {
	thumb, vid os.FileInfo
	ts         time.Time
}

func (c clip) String() string {
	return fmt.Sprintf("vid: %v, thumb: %v @ ts=%v", c.vid.Name(), c.thumb.Name(),
		c.ts.Format(time.RFC3339))
}

func parseClipInfo(name string) (int, time.Time) {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsNumber(r)
	})
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		log.Fatalf("error parsing clip info from %v (%v): %v", name, parts, err)
	}
	ts, err := time.ParseInLocation(clipTimeFmt, parts[1], time.Local)
	if err != nil {
		log.Fatalf("error parsing timestamp from %v (%v): %v", name, parts, err)
	}
	return id, ts
}

func main() {
	flag.Parse()

	d, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatalf("Can't open %v: %v", flag.Arg(0), err)
	}
	dents, err := d.Readdir(-1)
	if err != nil {
		log.Fatalf("Error in readdir: %v", err)
	}

	clips := map[int]clip{}

	for _, dent := range dents {
		log.Printf("read %v (%v bytes)", dent.Name(), dent.Size())
		if strings.HasSuffix(dent.Name(), ".avi") {
			id, ts := parseClipInfo(dent.Name())
			c := clips[id]
			c.vid = dent
			c.ts = ts
			clips[id] = c
		} else if strings.HasSuffix(dent.Name(), ".jpg") {
			id, _ := parseClipInfo(dent.Name())
			c := clips[id]
			c.thumb = dent
			clips[id] = c
		}
	}

	for id, clip := range clips {
		log.Printf("%v -> %v", id, clip)
	}
}
