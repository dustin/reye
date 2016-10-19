package main

import (
	"flag"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"strconv"
	"strings"
	"text/scanner"
	"time"
	"unicode"

	"github.com/dustin/go-humanize"
	"github.com/dustin/reye/vidtool"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"io"
	"path"

	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
)

const clipTimeFmt = "20060102150405"

var (
	cleanupFlag = flag.Bool("cleanup", false, "remove stuff when done")
	camid       = flag.String("camid", "", "Camera ID")
	authFile    = flag.String("authfile", "", "Path to auth json file")
	interval    = flag.Duration("duration", 30*time.Second, "How frequently to rescan")
	useSyslog   = flag.Bool("syslog", false, "Log to syslog")

	basePath string
)

type clip struct {
	thumb, ovid os.FileInfo
	details     map[string]string
	ts          time.Time
}

func (c clip) String() string {
	size := c.ovid.Size() + c.thumb.Size()
	return fmt.Sprintf("vid: %v, thumb: %v @ ts=%v (%v)", c.ovid.Name(), c.thumb.Name(),
		c.ts.Format(time.RFC3339), humanize.Bytes(uint64(size)))
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

func fq(fn string) string {
	return path.Join(basePath, fn)
}

func upload(ctx context.Context, sto *storage.Client, c clip) error {
	grp, _ := errgroup.WithContext(ctx)

	bucket := sto.Bucket("scenic-arc.appspot.com")

	up := func(fn string, ob *storage.ObjectHandle, attrs storage.ObjectAttrs) error {
		defer func(t time.Time) {
			log.Printf("Finished uploading %v in %v", fn, time.Since(t))
		}(time.Now())

		f, err := os.Open(fq(fn))
		if err != nil {
			return err
		}
		w := ob.NewWriter(ctx)
		w.ObjectAttrs.ContentType = attrs.ContentType
		w.ObjectAttrs.Metadata = attrs.Metadata
		_, err = io.Copy(w, f)
		if err != nil {
			return err
		}
		return w.Close()
	}

	grp.Go(func() error {
		oname := c.ts.Format(clipTimeFmt) + ".mp4"
		odur, err := vidtool.Transcode(fq(c.ovid.Name()), oname)
		if err != nil {
			return err
		}
		defer os.Remove(oname)

		vob := bucket.Object(path.Join(*camid, oname))
		vattrs := storage.ObjectAttrs{
			ContentType: "video/mp4",
			Metadata: map[string]string{
				"captured": c.ts.Format(time.RFC3339),
				"camera":   *camid,
				"duration": odur.String(),
			},
		}
		return up(oname, vob, vattrs)

	})

	tob := bucket.Object(path.Join(*camid, c.ts.Format(clipTimeFmt)+".jpg"))
	tattrs := storage.ObjectAttrs{
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"captured": c.ts.Format(time.RFC3339),
			"camera":   *camid,
		},
	}
	grp.Go(func() error { return up(c.thumb.Name(), tob, tattrs) })

	dur, err := vidtool.ClipDuration(fq(c.ovid.Name()))
	if err != nil {
		return err
	}
	ovob := bucket.Object(path.Join(*camid, c.ts.Format(clipTimeFmt)+".avi"))
	ovattrs := storage.ObjectAttrs{
		ContentType: "video/avi",
		Metadata: map[string]string{
			"captured": c.ts.Format(time.RFC3339),
			"camera":   *camid,
			"duration": dur.String(),
		},
	}
	grp.Go(func() error { return up(c.ovid.Name(), ovob, ovattrs) })

	return grp.Wait()
}

func initStorageClient(ctx context.Context) *storage.Client {
	client, err := storage.NewClient(ctx, option.WithServiceAccountFile(*authFile))
	if err != nil {
		log.Fatalf("Can't init storage client: %v", err)
	}
	return client
}

func cleanup(c clip) error {
	if !*cleanupFlag {
		return nil
	}

	if err := os.Remove(path.Join(basePath, c.thumb.Name())); err != nil {
		return err
	}

	if err := os.Remove(path.Join(basePath, c.ovid.Name())); err != nil {
		return err
	}

	return nil
}

func parseDetails(fn string) (int, map[string]string) {
	parts := strings.FieldsFunc(fn, func(r rune) bool {
		return !unicode.IsNumber(r)
	})
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		log.Fatalf("error parsing clip info from %v (%v): %v", fn, parts, err)
	}

	f, err := os.Open(fq(fn))
	if err != nil {
		log.Printf("Error opening details file %v: %v", fn, err)
		return id, nil
	}
	defer f.Close()

	rv := map[string]string{}

	s := scanner.Scanner{}
	s.Init(f)
	var tok rune
	for tok != scanner.EOF {
		tok = s.Scan()
		a := strings.SplitN(s.TokenText(), "=", 2)
		if len(a) == 2 {
			rv[a[0]] = rv[a[1]]
		}
	}

	return id, rv
}

func uploadAll(ctx context.Context, sto *storage.Client) {
	d, err := os.Open(basePath)
	if err != nil {
		log.Fatalf("Can't open %v: %v", basePath, err)
	}
	dents, err := d.Readdir(-1)
	if err != nil {
		log.Fatalf("Error in readdir: %v", err)
	}

	clips := map[int]clip{}

	for _, dent := range dents {
		dname := dent.Name()
		if dname[0] == '.' {
			// ignore dot files
		} else if strings.HasSuffix(dname, ".details") {
			id, details := parseDetails(dname)
			if details != nil {
				c := clips[id]
				c.details = details
				clips[id] = c
				log.Printf("Parsed details from %v: %v", dname, c.details)
			}
		} else if strings.HasSuffix(dname, ".avi") {
			id, ts := parseClipInfo(dname)
			c := clips[id]
			c.ovid = dent
			c.ts = ts
			clips[id] = c
		} else if strings.HasSuffix(dname, ".jpg") {
			id, _ := parseClipInfo(dname)
			c := clips[id]
			c.thumb = dent
			clips[id] = c
		}
	}

	for id, clip := range clips {
		if clip.thumb != nil && clip.ovid != nil && clip.details != nil {

			log.Printf("%v -> %v", id, clip)
			if err := upload(ctx, sto, clip); err != nil {
				log.Fatalf("Error uploading: %v", err)
			}
			if err := cleanup(clip); err != nil {
				log.Fatalf("Error cleaning up: %v", err)
			}
		}
	}
}

func main() {
	flag.Parse()

	if *useSyslog {
		sl, err := syslog.New(syslog.LOG_INFO, "uploader")
		if err != nil {
			log.Fatalf("Can't initialize syslog: %v", err)
		}
		log.SetOutput(sl)
		log.SetFlags(0)
	}

	ctx := context.Background()

	sto := initStorageClient(ctx)

	basePath = flag.Arg(0)

	uploadAll(ctx, sto)

	if *interval > 0 {
		for range time.Tick(*interval) {
			uploadAll(ctx, sto)
		}
	}

}
