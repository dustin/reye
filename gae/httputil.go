package scenic

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
)

func canGzip(req *http.Request) bool {
	acceptable := req.Header.Get("accept-encoding")
	return strings.Contains(acceptable, "gzip")
}

type gzippingWriter struct {
	gz     *gzip.Writer
	output io.Writer
}

func newGzippingWriter(w io.Writer, req *http.Request) *gzippingWriter {
	rv := &gzippingWriter{output: w}
	if canGzip(req) {
		if headered, ok := w.(http.ResponseWriter); ok {
			headered.Header().Set("Content-Encoding", "gzip")
		}
		rv.gz = gzip.NewWriter(w)
		rv.output = rv.gz
	}
	return rv
}

func (g *gzippingWriter) Write(b []byte) (int, error) {
	return g.output.Write(b)
}

func (g *gzippingWriter) Close() error {
	if g.gz != nil {
		return g.gz.Close()
	}
	return nil
}

func encodeJSONSlice(w io.Writer, thing interface{}) error {
	w.Write([]byte{'['})
	defer w.Write([]byte{']'})

	v := reflect.ValueOf(thing)
	for i := 0; i < v.Len(); i++ {
		item := v.Index(i).Interface()
		var j []byte
		var err error
		if it, ok := item.(json.Marshaler); ok {
			j, err = it.MarshalJSON()
		} else {
			j, err = json.Marshal(item)
		}
		if err != nil {
			return err
		}
		if _, err := w.Write(j); err != nil {
			return err
		}
		if i+1 < v.Len() {
			w.Write([]byte{','})
		}
	}

	return nil
}

func serveJSON(w io.Writer, req *http.Request, thing interface{}) error {
	if headered, ok := w.(http.ResponseWriter); ok {
		headered.Header().Set("Content-Type", "application/json")
	}

	out := newGzippingWriter(w, req)
	defer out.Close()

	if reflect.TypeOf(thing).Kind() == reflect.Slice {
		return encodeJSONSlice(out, thing)
	}

	return json.NewEncoder(out).Encode(thing)
}
