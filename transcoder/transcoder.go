package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/dustin/go-humanize"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"golang.org/x/net/context"
)

var (
	authFile   = flag.String("authfile", "", "Path to auth json file")
	bucketName = flag.String("bucket", "", "Bucket name")
	minRatio   = flag.Int("minRatio", 40, "Minimum percentage considered valid")

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

func findAll(ctx context.Context, sto *storage.Client) ([]*clip, error) {
	bucket := sto.Bucket(*bucketName)

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
		default:
			log.Printf("   Unknown %v (%v)", ob.Name, ob.ContentType)
		}
	}

	rv := make([]*clip, 0, len(m))
	for _, v := range m {
		if v.avi != nil && v.mp4 != nil {
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

func main() {
	flag.Parse()

	ctx := context.Background()

	sto := initStorageClient(ctx)

	clips, err := findAll(ctx, sto)
	if err != nil {
		log.Fatalf("Couldn't list stuff: %v", err)
	}

	for _, c := range clips {
		if int(100*c.ratio()) < *minRatio {
			log.Printf("Need to redo: %v", c)
		}
	}
}
