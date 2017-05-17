package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/syslog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dustin/go-humanize"
	"github.com/dustin/httputil"
	"github.com/dustin/reye/vidtool"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/option"
)

const clipTimeFmt = "20060102150405"

var (
	cleanupFlag = flag.Bool("cleanup", false, "remove stuff when done")
	camid       = flag.String("camid", "", "Camera ID")
	authFile    = flag.String("authfile", "", "Path to auth json file")
	interval    = flag.Duration("duration", 30*time.Second, "How frequently to rescan")
	useSyslog   = flag.Bool("syslog", false, "Log to syslog")
	bucketName  = flag.String("bucket", "scenic-arc.appspot.com", "your app/bucket name to store media")
	triggerAuth = flag.String("triggerAuth", "", "trigger auth token")
	triggerURL  = flag.String("triggerURL", "", "trigger URL")

	basePath string
)

type clip struct {
	thumb, ovid, df os.FileInfo
	details         map[string]string
	ts              time.Time
}

func (c clip) String() string {
	size := c.ovid.Size() + c.thumb.Size()
	return fmt.Sprintf("vid: %v, thumb: %v @ ts=%v (%v)", c.ovid.Name(), c.thumb.Name(),
		c.ts.Format(time.RFC3339), humanize.Bytes(uint64(size)))
}

func parseClipInfo(name string) (int, time.Time, error) {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsNumber(r)
	})
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parsing clip info from  %v (%v): %v", name, parts, err)
	}
	ts, err := time.ParseInLocation(clipTimeFmt, parts[1], time.Local)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parsing timestamp from %v (%v): %v", name, parts, err)
	}
	return id, ts, nil
}

func fq(fn string) string {
	return path.Join(basePath, fn)
}

func uploadOne(ctx context.Context, fn string, c clip, ob *storage.ObjectHandle, attrs storage.ObjectAttrs) error {
	defer func(t time.Time) {
		log.Printf("Finished uploading %v in %v", fn, time.Since(t))
	}(time.Now())

	f, err := os.Open(fq(fn))
	if err != nil {
		return err
	}
	w := ob.NewWriter(ctx)
	w.ObjectAttrs.ContentType = attrs.ContentType
	w.ObjectAttrs.Metadata = map[string]string{}
	for k, v := range c.details {
		w.ObjectAttrs.Metadata[k] = v
	}
	for k, v := range attrs.Metadata {
		w.ObjectAttrs.Metadata[k] = v
	}
	_, err = io.Copy(w, f)
	if err != nil {
		return err
	}
	return w.Close()
}

func upload(ctx context.Context, sto *storage.Client, c clip) error {
	grp, _ := errgroup.WithContext(ctx)

	bucket := sto.Bucket(*bucketName)

	grp.Go(func() error {
		oname := c.ts.Format(clipTimeFmt) + ".mp4"
		odur, err := vidtool.Transcode(ctx, fq(c.ovid.Name()), fq(oname))
		if err != nil {
			return err
		}
		defer os.Remove(fq(oname))

		vob := bucket.Object(path.Join(*camid, oname))
		vattrs := storage.ObjectAttrs{
			ContentType: "video/mp4",
			Metadata: map[string]string{
				"captured": c.ts.Format(time.RFC3339),
				"camera":   *camid,
				"duration": odur.String(),
			},
		}
		return uploadOne(ctx, oname, c, vob, vattrs)

	})

	tob := bucket.Object(path.Join(*camid, c.ts.Format(clipTimeFmt)+".jpg"))
	tattrs := storage.ObjectAttrs{
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"captured": c.ts.Format(time.RFC3339),
			"camera":   *camid,
		},
	}
	grp.Go(func() error { return uploadOne(ctx, c.thumb.Name(), c, tob, tattrs) })

	dur, err := vidtool.ClipDuration(ctx, fq(c.ovid.Name()))
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
	grp.Go(func() error { return uploadOne(ctx, c.ovid.Name(), c, ovob, ovattrs) })

	if err := grp.Wait(); err != nil {
		return err
	}

	if *triggerURL != "" {
		req, err := http.NewRequest("POST", *triggerURL, strings.NewReader(
			"cam="+*camid+"&id="+c.ts.Format(clipTimeFmt)))
		if err != nil {
			return err
		}
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		req.Header.Set("x-reye", *triggerAuth)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Error sending trigger: %v", err)
			return nil
		}
		defer res.Body.Close()
		if res.StatusCode != 201 {
			log.Printf("Error executing trigger: %v", httputil.HTTPError(res))
			return nil
		}

		log.Printf("Upload notification succeeded")
	}

	return nil
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

	if err := os.Remove(fq(c.thumb.Name())); err != nil {
		return err
	}

	if err := os.Remove(fq(c.ovid.Name())); err != nil {
		return err
	}

	if err := os.Remove(fq(c.df.Name())); err != nil {
		return err
	}

	return nil
}

func parseMap(r io.Reader) map[string]string {
	rv := map[string]string{}

	s := bufio.NewScanner(r)
	s.Split(bufio.ScanWords)

	for s.Scan() {
		a := strings.SplitN(s.Text(), "=", 2)
		if len(a) == 2 {
			rv[a[0]] = a[1]
		}

	}
	if err := s.Err(); err != nil {
		log.Printf("scanning details: %v", err)
	}

	return rv
}

func parseDetails(fn string) (int, map[string]string, error) {
	parts := strings.FieldsFunc(fn, func(r rune) bool {
		return !unicode.IsNumber(r)
	})
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, nil, fmt.Errorf("parsing clip info from %v (%v): %v", fn, parts, err)
	}

	f, err := os.Open(fq(fn))
	if err != nil {
		return id, nil, err
	}
	defer f.Close()

	return id, parseMap(f), nil
}

func uploadSnapshot(ctx context.Context, sto *storage.Client) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	bucket := sto.Bucket(*bucketName)

	ovob := bucket.Object(path.Join(*camid, "lastsnap.jpg"))
	ovattrs := storage.ObjectAttrs{
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"camera": *camid,
		},
	}
	return uploadOne(ctx, "lastsnap.jpg", clip{}, ovob, ovattrs)
}

func uploadAll(ctx context.Context, sto *storage.Client) error {
	d, err := os.Open(basePath)
	if err != nil {
		return err
	}
	dents, err := d.Readdir(-1)
	if err != nil {
		return err
	}

	clips := map[int]clip{}

	var snaps []string

	for _, dent := range dents {
		dname := dent.Name()
		if dname[0] == '.' {
			// ignore dot files
		} else if dname == "lastsnap.jpg" {
			// Upload the latest snapshot separately
			if err := uploadSnapshot(ctx, sto); err != nil {
				log.Printf("Error uploading the latest snapshot: %v", err)
				continue
			}
			snaps = append(snaps, fq(dname))
		} else if strings.HasSuffix(dname, ".details") {
			id, details, err := parseDetails(dname)
			if err != nil {
				log.Printf("error parsing %v: %v", dname, err)
				continue
			}
			c := clips[id]
			c.df = dent
			c.details = details
			clips[id] = c
			log.Printf("Parsed details from %v: %v", dname, c.details)
		} else if strings.HasSuffix(dname, ".avi") {
			id, ts, err := parseClipInfo(dname)
			if err != nil {
				log.Printf("error parsing %v: %v", dname, err)
				continue
			}
			c := clips[id]
			c.ovid = dent
			c.ts = ts
			clips[id] = c
		} else if strings.HasSuffix(dname, "-snapshot.jpg") {
			// just a snapshot, will handle separately.
			snaps = append(snaps, fq(dname))
		} else if strings.HasSuffix(dname, ".jpg") {
			id, _, err := parseClipInfo(dname)
			if err != nil {
				log.Printf("error parsing %v: %v", dname, err)
			}
			c := clips[id]
			c.thumb = dent
			clips[id] = c
		}
	}

	for _, s := range snaps {
		if err := os.Remove(s); err != nil {
			log.Printf("Error deleting %q: %v", s, err)
		}
	}

	for id, clip := range clips {
		if clip.thumb != nil && clip.ovid != nil && clip.details != nil {
			log.Printf("%v -> %v", id, clip)
			if err := upload(ctx, sto, clip); err != nil {
				return fmt.Errorf("uploading %v: %v", clip, err)
			}
			if err := cleanup(clip); err != nil {
				return fmt.Errorf("cleaning up %v: %v", clip, err)
			}
		}
	}
	return nil
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

	if err := uploadAll(ctx, sto); err != nil {
		log.Printf("Error uploading: %v", err)
		if *interval == 0 {
			os.Exit(1)
		}
	}

	if *interval > 0 {
		for range time.Tick(*interval) {
			if err := uploadAll(ctx, sto); err != nil {
				log.Printf("Error uploading stuff: %v", err)
			}
		}
	}

}
