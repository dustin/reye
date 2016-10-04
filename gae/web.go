package scenic

import (
	"html/template"
	"io"
	"net/http"

	"golang.org/x/net/context"

	"google.golang.org/appengine"
	"google.golang.org/appengine/log"
)

var (
	templates = template.Must(template.New("").ParseGlob("templates/*"))
)

func init() {
	http.HandleFunc("/eye/", handleHome)

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

func handleHome(w http.ResponseWriter, r *http.Request) {
	execTemplate(appengine.NewContext(r), w, "app.html", nil)
}
