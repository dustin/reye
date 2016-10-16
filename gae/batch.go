package scenic

import (
	"net/http"
	"strings"

	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/file"
	"google.golang.org/appengine/log"
)

const clipTimeFmt = "20060102150405"

var localTime *time.Location

func init() {
	http.HandleFunc("/batch/scan", handleBatchScan)

	var err error
	localTime, err = time.LoadLocation("US/Pacific")
	if err != nil {
		// ... do something
		localTime = time.Local
	}
}

func handleBatchScan(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	q := datastore.NewQuery("Camera").KeysOnly()
	keys := map[string]*datastore.Key{}
	for it := q.Run(c); ; {
		k, err := it.Next(nil)
		if err == datastore.Done {
			break
		}
		keys[k.StringID()] = k
	}

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
		return
	}

	bucket := client.Bucket(bucketName)
	log.Infof(c, "Listing bucket")
	var keystodo []*datastore.Key
	var valstodo []interface{}
	todo := 0

	it := bucket.Objects(c, nil)
	for {
		ob, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Errorf(c, "Error iterating bucket: %v", err)
		}
		if ob.ContentType == "image/jpeg" {
			// basement/20161013173815.jpg
			pp := strings.Split(ob.Name, "/")
			camkey, ok := keys[pp[0]]
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
			log.Infof(c, "Adding %v in %v: %v", fp[0], camkey, t)

			keystodo = append(keystodo, datastore.NewKey(c, "Event", pp[0]+"/"+fp[0], 0, nil))
			valstodo = append(valstodo, &Event{
				Camera:    camkey,
				Timestamp: t,
				Filename:  fp[0],
			})
			todo++
		}
	}
	log.Infof(c, "Completed listing of %d items", todo)

	for len(keystodo) > 0 {
		n := len(keystodo)
		if n >= 500 {
			n = 500
		}
		_, err = datastore.PutMulti(c, keystodo[:n], valstodo[:n])
		if err != nil {
			log.Errorf(c, "Error storing items: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		log.Infof(c, "Stored %v items", n)
		keystodo = keystodo[n:]
		valstodo = valstodo[n:]
	}

	w.Write([]byte("ok"))
}
