package scenic

import (
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"reflect"

	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
)

var (
	templates = template.Must(template.New("").ParseGlob("templates/*"))
)

func init() {
	http.HandleFunc("/eye/", handleHome)

	http.HandleFunc("/api/recentImages", handleRecentImages)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/eye/", http.StatusFound)
	})
}

func execTemplate(c context.Context, w io.Writer, name string, obj interface{}) error {
	err := templates.ExecuteTemplate(w, name, obj)

	if err != nil {
		log.Errorf(c, "Error executing template %v: %v", name, err)
		if wh, ok := w.(http.ResponseWriter); ok {
			http.Error(wh, "Error executing template", 500)
		}
	}
	return err
}

func mustEncode(c context.Context, w io.Writer, req *http.Request, i interface{}) {
	if headered, ok := w.(http.ResponseWriter); ok {
		headered.Header().Set("Cache-Control", "no-cache")
		headered.Header().Set("Content-type", "application/json")
	}

	out := newGzippingWriter(w, req)
	defer out.Close()

	if reflect.TypeOf(i).Kind() == reflect.Slice {
		if err := encodeJSONSlice(out, i); err != nil {
			log.Errorf(c, "Error json encoding: %v", err)
			if h, ok := w.(http.ResponseWriter); ok {
				http.Error(h, err.Error(), 500)
			}
		}
		return
	}

	if err := json.NewEncoder(out).Encode(i); err != nil {
		log.Errorf(c, "Error json encoding: %v", err)
		if h, ok := w.(http.ResponseWriter); ok {
			http.Error(h, err.Error(), 500)
		}
	}
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	execTemplate(appengine.NewContext(r), w, "app.html", nil)
}

func handleRecentImages(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	rv := []Event{}

	q := datastore.NewQuery("Event").Order("-ts").Limit(90)
	if err := fillKeyQuery(c, q, &rv); err != nil {
		log.Errorf(c, "Error fetching recent images: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}

	mustEncode(c, w, r, rv)
}
