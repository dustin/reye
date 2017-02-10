package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/dustin/reye/vidtool"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

var (
	authFile          = flag.String("authfile", "", "Path to auth json file")
	bucketName        = flag.String("bucket", "", "Bucket name")
	minRatio          = flag.Int("minRatio", 40, "Minimum percentage considered valid")
	onlyBroken        = flag.Bool("onlybroken", false, "Only update obviously broken outputs")
	filterConcurrency = flag.Int("filter_concurrency", 8, "How many filters to run concurrently")

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
		if v.avi != nil {
			rv = append(rv, v)
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

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func getOrigDuration(ctx context.Context, bucket *storage.BucketHandle, c *clip) (time.Duration, error) {
	if c.mp4 == nil {
		return 0, fmt.Errorf("no mp4 found")
	}
	obj := bucket.Object(c.mp4.Name)
	if c.mp4.Metadata["duration"] != "" {
		return time.ParseDuration(c.mp4.Metadata["duration"])
	}

	r, err := obj.NewReader(ctx)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	oname := url.QueryEscape(c.mp4.Name)
	tmpf, err := os.Create(oname)
	if err != nil {
		return 0, err
	}
	defer tmpf.Close()
	defer os.Remove(oname)
	dur, err := vidtool.ClipDuration(ctx, oname)
	if err != nil {
		return 0, err
	}

	newattrs := storage.ObjectAttrsToUpdate{
		Metadata: c.mp4.Metadata,
	}
	newattrs.Metadata["duration"] = dur.String()
	if _, err = obj.Update(ctx, newattrs); err != nil {
		log.Printf("Error updating mp4 duration: %v", err)
	}
	return dur, nil
}

func transcode(ctx context.Context, bucket *storage.BucketHandle, c *clip) error {
	grp, _ := errgroup.WithContext(ctx)

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

	idur, err := vidtool.ClipDuration(ctx, iname)
	if err != nil {
		return err
	}

	if c.avi.Metadata["duration"] == "" {
		grp.Go(func() error {
			newattrs := storage.ObjectAttrsToUpdate{
				Metadata: c.avi.Metadata,
			}
			newattrs.Metadata["duration"] = idur.String()
			_, err := obj.Update(ctx, newattrs)
			return err
		})
	}

	if !*onlyBroken {
		odur, err := getOrigDuration(ctx, bucket, c)
		if err != nil {
			log.Printf("Error getting original clip duration: %v", err)
			odur = 0
		}

		if abs(odur-idur) < time.Second {
			log.Printf("Skipping %v, since it's roughly the same size (%v vs. %v)",
				c, idur, odur)
			return nil
		}
	}

	odur, err := vidtool.Transcode(ctx, iname, oname)
	defer os.Remove(oname)
	if err != nil {
		return err
	}

	grp.Go(func() error {
		dest := bucket.Object(c.mp4.Name)
		w := dest.NewWriter(ctx)
		w.ObjectAttrs.Metadata = c.mp4.Metadata
		w.ObjectAttrs.ContentType = c.mp4.ContentType
		w.ObjectAttrs.Metadata["duration"] = odur.String()

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
	})

	return grp.Wait()
}

func filter(ctx context.Context, bucket *storage.BucketHandle, clips []*clip) chan *clip {
	grp, _ := errgroup.WithContext(ctx)

	ch := make(chan *clip)
	sem := make(chan bool, *filterConcurrency)
	go func() {
		defer close(ch)
		for _, c := range clips {
			c := c
			if *onlyBroken {
				grp.Go(func() error {
					sem <- true
					defer func() { <-sem }()
					if _, err := getOrigDuration(ctx, bucket, c); err != nil {
						ch <- c
					}
					return nil
				})
			} else if int(100*c.ratio()) < *minRatio {
				ch <- c
			}
		}
		grp.Wait()
	}()
	return ch
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

	i := 0
	for c := range filter(ctx, bucket, clips) {
		if err := transcode(ctx, bucket, c); err != nil {
			log.Fatalf("Error transcoding %v: %v", c, err)
		}
		i++
	}
	log.Printf("Updated %v clips", i)
}
