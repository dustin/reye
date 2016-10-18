package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/dustin/go-humanize"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"os/exec"

	"net/url"

	"golang.org/x/net/context"
)

var (
	authFile   = flag.String("authfile", "", "Path to auth json file")
	bucketName = flag.String("bucket", "", "Bucket name")
	minRatio   = flag.Int("minRatio", 40, "Minimum percentage considered valid")
	ffmpeg     = flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	ffprobe    = flag.String("ffprobe", "ffprobe", "path to ffprobe")

	basePath string
)

type clip struct {
	name     string
	avi, mp4 *storage.ObjectAttrs
}

func (c clip) ratio() float64 {
	return float64(c.mp4.Size) / float64(c.avi.Size)
}

func (c clip) String() string {
	return fmt.Sprintf("%v: avi=%v, mp4=%v (%.2f%%)", c.name,
		humanize.Bytes(uint64(c.avi.Size)), humanize.Bytes(uint64(c.mp4.Size)),
		100*c.ratio())
}

func findAll(ctx context.Context, bucket *storage.BucketHandle) ([]*clip, error) {
	m := map[string]*clip{}
	it := bucket.Objects(ctx, nil)
	for {
		ob, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		n := ob.Name[:len(ob.Name)-4]
		e, ok := m[n]
		if !ok {
			e = &clip{name: n}
			m[n] = e
		}
		switch ob.ContentType {
		case "video/mp4":
			e.mp4 = ob
		case "video/avi":
			e.avi = ob
		case "image/jpeg":
			// don't care
		default:
			log.Printf("   Unknown %v (%v)", ob.Name, ob.ContentType)
		}
	}

	rv := make([]*clip, 0, len(m))
	for _, v := range m {
		if v.avi != nil && v.mp4 != nil {
			if int(100*v.ratio()) < *minRatio {
				rv = append(rv, v)
			}
		}
	}

	return rv, nil
}

func initStorageClient(ctx context.Context) *storage.Client {
	client, err := storage.NewClient(ctx, option.WithServiceAccountFile(*authFile))
	if err != nil {
		log.Fatalf("Can't init storage client: %v", err)
	}
	return client
}

func getClipDuration(fn string) (time.Duration, error) {
	cmd := exec.Command(*ffprobe, "-v", "quiet", "-print_format", "json",
		"-show_format", "-show_streams", fn)
	o, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	info := struct {
		Format struct {
			Duration string
		}
	}{}

	if err := json.Unmarshal(o, &info); err != nil {
		return 0, err
	}

	return time.ParseDuration(info.Format.Duration + "s")
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func transcode(ctx context.Context, bucket *storage.BucketHandle, c *clip) error {
	log.Printf("Transcoding %v", c)
	start := time.Now()
	obj := bucket.Object(c.avi.Name)
	r, err := obj.NewReader(ctx)
	if err != nil {
		return err
	}
	defer r.Close()

	iname := url.QueryEscape(c.avi.Name)
	oname := url.QueryEscape(c.mp4.Name)

	tmpf, err := os.Create(iname)
	if err != nil {
		return err
	}
	defer tmpf.Close()
	defer os.Remove(iname)
	if _, err := io.Copy(tmpf, r); err != nil {
		return err
	}

	idur, err := getClipDuration(iname)
	if err != nil {
		return err
	}

	cmd := exec.Command(*ffmpeg, "-i", iname, oname)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return err
	}
	defer os.Remove(oname)

	odur, err := getClipDuration(oname)
	if err != nil {
		return err
	}

	if abs(odur-idur) > time.Second {
		return fmt.Errorf("durations inconsistent, in=%v, out=%v", idur, odur)
	}

	dest := bucket.Object(c.mp4.Name)
	w := dest.NewWriter(ctx)
	w.ObjectAttrs.Metadata = c.mp4.Metadata
	w.ObjectAttrs.ContentType = c.mp4.ContentType

	f, err := os.Open(oname)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(w, f)
	if err != nil {
		return err
	}

	log.Printf("Downloaded, transcoded, and uploaded %v bytes in %v",
		humanize.Bytes(uint64(n)), time.Since(start))

	return w.Close()
}

func main() {
	flag.Parse()

	ctx := context.Background()

	sto := initStorageClient(ctx)
	bucket := sto.Bucket(*bucketName)

	clips, err := findAll(ctx, bucket)
	if err != nil {
		log.Fatalf("Couldn't list stuff: %v", err)
	}

	log.Printf("Transcoding %v clips", len(clips))
	for _, c := range clips {
		if err := transcode(ctx, bucket, c); err != nil {
			log.Fatalf("Error transcoding %v: %v", c, err)
		}
	}
}
