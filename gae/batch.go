package scenic

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"

	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/file"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/mail"
	"google.golang.org/appengine/taskqueue"
)

const (
	clipTimeFmt    = "20060102150405"
	maxSnapAge     = time.Hour
	snapWarningAge = time.Minute * 25
)

var localTime *time.Location

func init() {
	http.HandleFunc("/batch/scan", handleBatchScan)
	http.HandleFunc("/batch/scanAll", handleBatchScanAll)
	http.HandleFunc("/batch/scanSnaps", handleBatchScanSnaps)
	http.HandleFunc("/batch/expunge", handleBatchExpunge)

	var err error
	localTime, err = time.LoadLocation("US/Pacific")
	if err != nil {
		// ... do something
		localTime = time.Local
	}
}

func handleBatchScanSnaps(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	camgrp := errgroup.Group{}
	camkeys := map[string]*datastore.Key{}

	camgrp.Go(func() error {
		cams, err := loadCameras(c)
		if err != nil {
			return err
		}
		for _, c := range cams {
			camkeys[c.Key.StringID()] = c.Key
		}
		log.Debugf(c, "Loaded %v cameras", len(camkeys))
		return nil
	})

	client, err := storage.NewClient(c)
	if err != nil {
		log.Warningf(c, "Error getting cloud store interface:  %v", err)
		http.Error(w, "error talking to cloud store", 500)
		return

	}
	defer client.Close()

	var bucketName string
	if bucketName, err = file.DefaultBucketName(c); err != nil {
		log.Errorf(c, "failed to get default GCS bucket name: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}

	bucket := client.Bucket(bucketName)

	oq := &storage.Query{
		Prefix: "__snaps",
	}
	log.Debugf(c, "Listing bucket with query %#v", oq)

	grp := errgroup.Group{}
	sem := make(chan bool, 10)
	deleting := 0

	recents := map[string]time.Time{}

	it := bucket.Objects(c, oq)
	for {
		ob, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Errorf(c, "Error iterating bucket: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}

		// Before we proceed, we should have our cameras loaded.
		if err := camgrp.Wait(); err != nil {
			log.Errorf(c, "Failed to initialize cams and stuff: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}

		// __snaps/basement/20170518205540.jpg
		pp := strings.Split(ob.Name, "/")
		/*
			camkey, ok := camkeys[pp[1]]
			if !ok {
				log.Warningf(c, "Unhandled key: %v from %v", pp[1], ob.Name)
				continue
			}
		*/
		fp := strings.Split(pp[2], ".")
		t, err := time.Parse(time.RFC3339, ob.Metadata["captured"])
		if err != nil {
			t, err = time.ParseInLocation(clipTimeFmt, fp[0], localTime)
			if err != nil {
				log.Infof(c, "Failed to parse time in %v: %v", ob.Name, err)
				continue
			}
		}

		if recents[pp[1]].Before(t) {
			recents[pp[1]] = t
		}

		if time.Since(t) > maxSnapAge {
			deleting++
			grp.Go(func() error {
				sem <- true
				defer func() { <-sem }()
				log.Debugf(c, "Deleting %v (%v old)", ob.Name, time.Since(t))
				return bucket.Object(ob.Name).Delete(c)
			})
		}
	}

	log.Infof(c, "Deleting %v snapshots.", deleting)
	var notify []string
	for k, v := range recents {
		log.Infof(c, "Most recent %v: %v (%v ago)", k, v, time.Since(v))
		if time.Since(v) > snapWarningAge {
			notify = append(notify, k)
		}
	}

	if len(notify) > 0 {
		grp.Go(func() error {
			return notifyOldCams(c, notify, recents)
		})
	}

	if err := grp.Wait(); err != nil {
		log.Errorf(c, "Error deleting snapshots: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}

}

func notifyOldCams(ctx context.Context, cams []string, recents map[string]time.Time) error {
	m := map[string]time.Time{}
	for _, k := range cams {
		m[k] = recents[k]
	}

	buf := &bytes.Buffer{}
	if err := templates.ExecuteTemplate(buf, "oldsnaps.txt", m); err != nil {
		return err
	}

	msg := &mail.Message{
		Sender:  "Dustin Sallings <dsallings@gmail.com>",
		To:      []string{"dustin@sallings.org"},
		Subject: "Camera Not Snapshotting",
		Body:    string(buf.Bytes()),
	}
	log.Infof(ctx, "Sending:\n%s\n", msg.Body)
	return mail.Send(ctx, msg)
}

func handleBatchScanAll(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	cams, err := loadCameras(c)
	if err != nil {
		log.Warningf(c, "Error listing cameras:  %v", err)
		http.Error(w, "error listing cameras", 500)
		return
	}

	grp := errgroup.Group{}

	for _, cam := range cams {
		cam := cam
		grp.Go(func() error {
			log.Infof(c, "Requesting scan of %v", cam.Key.StringID())
			_, err := taskqueue.Add(c, taskqueue.NewPOSTTask("/batch/scan", url.Values{"subdir": []string{cam.Key.StringID()}}), "")
			return err
		})
	}

	if err := grp.Wait(); err != nil {
		log.Warningf(c, "Error queueing batch scans:  %v", err)
		http.Error(w, "error queueing batch scans", 500)
		return
	}

	w.WriteHeader(204)
}

func handleBatchScan(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	camkeys := map[string]*datastore.Key{}
	evkeys := map[string]bool{}

	grp, _ := errgroup.WithContext(c)

	subdir := r.FormValue("subdir")

	grp.Go(func() error {
		cams, err := loadCameras(c)
		if err != nil {
			return err
		}
		for _, c := range cams {
			camkeys[c.Key.StringID()] = c.Key
		}
		if _, ok := camkeys[subdir]; subdir != "" && !ok {
			return fmt.Errorf("Requested camera %q not found in %v", subdir, camkeys)
		}
		log.Debugf(c, "Loaded %v cameras", len(camkeys))
		return nil
	})

	grp.Go(func() error {
		q := datastore.NewQuery("Event").KeysOnly()
		if subdir != "" {
			log.Debugf(c, "Limiting search to cam %v", subdir)
			q = q.Filter("camera =", datastore.NewKey(c, "Camera", subdir, 0, nil))
		}

		for it := q.Run(c); ; {
			k, err := it.Next(nil)
			if err == datastore.Done {
				break
			} else if err != nil {
				return err
			}
			evkeys[k.StringID()] = true
		}
		log.Debugf(c, "Loaded %v events", len(evkeys))
		return nil
	})

	client, err := storage.NewClient(c)
	if err != nil {
		log.Warningf(c, "Error getting cloud store interface:  %v", err)
		http.Error(w, "error talking to cloud store", 500)
		return

	}
	defer client.Close()

	var bucketName string
	if bucketName, err = file.DefaultBucketName(c); err != nil {
		log.Errorf(c, "failed to get default GCS bucket name: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}

	bucket := client.Bucket(bucketName)
	var keystodo []*datastore.Key
	var valstodo []interface{}
	todo := 0

	var oq *storage.Query
	if subdir != "" {
		oq = &storage.Query{
			Prefix: subdir,
		}
	}
	log.Debugf(c, "Listing bucket with query %#v", oq)

	it := bucket.Objects(c, oq)
	for {
		ob, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Errorf(c, "Error iterating bucket: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		if ob.ContentType == "video/mp4" {
			// basement/20161013173815.jpg
			if err := grp.Wait(); err != nil {
				log.Errorf(c, "failed to get default GCS bucket name: %v", err)
				http.Error(w, err.Error(), 500)
				return
			}
			pp := strings.Split(ob.Name, "/")
			camkey, ok := camkeys[pp[0]]
			if !ok {
				log.Warningf(c, "Unhandled key: %v from %v", pp[0], ob.Name)
				continue
			}
			fp := strings.Split(pp[1], ".")
			t, err := time.Parse(time.RFC3339, ob.Metadata["captured"])
			if err != nil {
				t, err = time.ParseInLocation(clipTimeFmt, fp[0], localTime)
				if err != nil {
					log.Infof(c, "Failed to parse time in %v: %v", ob.Name, err)
					continue
				}
			}

			dur, err := time.ParseDuration(ob.Metadata["duration"])
			if err != nil {
				log.Infof(c, "No duration for %v: %v", fp[0], err)
				continue
			}
			var md []struct{ K, V string }
			for k, v := range ob.Metadata {
				switch k {
				case "", "camera", "captured", "duration":
				default:
					md = append(md, struct{ K, V string }{k, v})
				}
			}

			evkey := datastore.NewKey(c, "Event", pp[0]+"/"+fp[0], 0, nil)

			if !evkeys[evkey.StringID()] {
				log.Debugf(c, "Adding %v in %v: %v", fp[0], camkey, t)

				keystodo = append(keystodo, evkey)
				valstodo = append(valstodo, &Event{
					Camera:    camkey,
					Timestamp: t,
					Filename:  fp[0],
					Duration:  dur,
					Metadata:  md,
				})
				todo++
			}
		}
	}
	log.Debugf(c, "Completed listing of %d items", todo)

	grp, _ = errgroup.WithContext(c)

	for len(keystodo) > 0 {
		n := len(keystodo)
		if n >= 500 {
			n = 500
		}
		tk := keystodo[:n]
		tv := valstodo[:n]
		grp.Go(func() error {
			_, err = datastore.PutMulti(c, tk, tv)
			return err
		})
		log.Debugf(c, "Stored %v items", n)
		keystodo = keystodo[n:]
		valstodo = valstodo[n:]
	}

	if err := grp.Wait(); err != nil {
		log.Errorf(c, "Error storing items: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}

	w.WriteHeader(204)
}

func handleBatchExpunge(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	var bucketName string
	var err error
	if bucketName, err = file.DefaultBucketName(c); err != nil {
		log.Errorf(c, "failed to get default GCS bucket name: %v", err)
		return
	}

	client, err := storage.NewClient(c)
	if err != nil {
		log.Warningf(c, "Error getting cloud store interface:  %v", err)
		http.Error(w, "error talking to cloud store", 500)
		return

	}
	defer client.Close()

	bucket := client.Bucket(bucketName)

	ts := time.Now().Add(time.Hour * 24 * -30)
	expunging := 0
	grp, _ := errgroup.WithContext(c)

	sem := make(chan bool, 10)

	q := datastore.NewQuery("Event").Filter("ts <", ts).Limit(500)
	for it := q.Run(c); ; {
		ev := Event{}
		k, err := it.Next(&ev)
		if err == datastore.Done {
			break
		} else if err != nil {
			log.Errorf(c, "Error fetching events: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		expunging++
		grp.Go(func() error {
			sem <- true
			defer func() { <-sem }()
			log.Debugf(c, "Expunging %v", ev.Filename)

			exts := []string{"jpg", "mp4", "avi"}

			for _, ext := range exts {
				fn := ev.Camera.StringID() + "/" + ev.Filename + "." + ext
				o := bucket.Object(fn)
				if err := o.Delete(c); err != nil {
					log.Warningf(c, "Error deleting %v: %v", fn, err)
				}
			}

			return datastore.Delete(c, k)
		})
	}

	log.Infof(c, "Expunging %v entries", expunging)

	if err := grp.Wait(); err != nil {
		log.Errorf(c, "Error expunging events: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}

	w.WriteHeader(204)
}
