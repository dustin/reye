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

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"io"
	"path"

	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
)

const clipTimeFmt = "20060102150405"

var (
	cleanup  = flag.Bool("cleanup", false, "remove stuff when done")
	camid    = flag.String("camid", "", "Camera ID")
	authFile = flag.String("authfile", "", "Path to auth json file")
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

func upload(ctx context.Context, sto *storage.Client, c clip) error {
	grp, _ := errgroup.WithContext(ctx)

	bucket := sto.Bucket("scenic-arc.appspot.com")
	vob := bucket.Object(path.Join(*camid, c.ts.Format(clipTimeFmt)+".mp4"))
	vattrs := storage.ObjectAttrs{
		ContentType: "video/mp4",
		Metadata: map[string]string{
			"captured": c.ts.Format(time.RFC3339),
			"camera":   *camid,
		},
	}
	tob := bucket.Object(path.Join(*camid, c.ts.Format(clipTimeFmt)+".jpg"))
	tattrs := storage.ObjectAttrs{
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"captured": c.ts.Format(time.RFC3339),
			"camera":   *camid,
		},
	}

	up := func(fn string, ob *storage.ObjectHandle, attrs storage.ObjectAttrs) error {
		f, err := os.Open(path.Join(flag.Arg(0), fn))
		if err != nil {
			return err
		}
		w := ob.NewWriter(ctx)
		_, err = io.Copy(w, f)
		if err != nil {
			return err
		}
		log.Printf("Closing %v", fn)
		if err := w.Close(); err != nil {
			return err
		}
		// log.Printf("Updating attrs for %v", fn)
		// _, err = vob.Update(ctx, attrs)
		// return err
		return nil
	}

	grp.Go(func() error { return up(c.vid.Name(), vob, vattrs) })
	grp.Go(func() error { return up(c.thumb.Name(), tob, tattrs) })

	return grp.Wait()
}

func initStorageClient(ctx context.Context) *storage.Client {
	client, err := storage.NewClient(ctx, option.WithServiceAccountFile(*authFile))
	if err != nil {
		log.Fatalf("Can't init storage client: %v", err)
	}
	return client
}

func main() {
	flag.Parse()

	ctx := context.Background()

	sto := initStorageClient(ctx)

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
		if strings.HasSuffix(dent.Name(), ".mp4") {
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
		if clip.thumb != nil && clip.vid != nil {
			if err := upload(ctx, sto, clip); err != nil {
				log.Fatalf("Error uploading: %v", err)
			}
		}
	}
}
