package scenic

import (
	"encoding/json"
	"reflect"
	"time"

	"context"

	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/memcache"
)

// Camera represents a physical camera capturing content.
type Camera struct {
	Name string `json:"name" datastore:"name"`

	Key *datastore.Key `datastore:"-"`
}

func (c *Camera) setKey(to *datastore.Key) {
	c.Key = to
}

// MarshalJSON JSONifies cameras.
func (c Camera) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{
		"name":  c.Name,
		"key":   c.Key.Encode(),
		"keyid": c.Key.StringID(),
	}
	return json.Marshal(m)
}

// An Event represents all of the details of a time when motion was detected.
type Event struct {
	Camera    *datastore.Key          `json:"cam_id" datastore:"camera"`
	Timestamp time.Time               `json:"ts" datastore:"ts"`
	Filename  string                  `json:"fn" datastore:"fn"`
	Duration  time.Duration           `json:"duration"`
	Metadata  []struct{ K, V string } `json:"metadata"`

	Key *datastore.Key `datastore:"-"`
}

func (u *Event) setKey(to *datastore.Key) {
	u.Key = to
}

// Keyable entities can have their keys set via fillKeyQuery
type Keyable interface {
	setKey(*datastore.Key)
}

func fillKeyQuery(c context.Context, q *datastore.Query, results interface{}) error {
	keys, err := q.GetAll(c, results)
	if err == nil {
		rslice := reflect.ValueOf(results).Elem()
		for i := range keys {
			if k, ok := rslice.Index(i).Interface().(Keyable); ok {
				k.setKey(keys[i])
			} else if k, ok := rslice.Index(i).Addr().Interface().(Keyable); ok {
				k.setKey(keys[i])
			} else {
				log.Infof(c, "Warning: %v is not Keyable", rslice.Index(i).Interface())
			}
		}
	} else {
		log.Errorf(c, "Error executing query: %v", err)
	}
	return err
}

const camsKey = "cameras"

func loadCameras(c context.Context) (map[string]*Camera, error) {
	rv := map[string]*Camera{}

	_, err := memcache.JSON.Get(c, camsKey, &rv)
	if err != nil {
		q := datastore.NewQuery("Camera")
		for it := q.Run(c); ; {
			cam := &Camera{}
			k, err := it.Next(cam)
			if err == datastore.Done {
				break
			} else if err != nil {
				return nil, err
			}
			cam.setKey(k)
			rv[k.StringID()] = cam
		}

		memcache.JSON.Set(c, &memcache.Item{
			Key:        camsKey,
			Object:     rv,
			Expiration: time.Hour * 24,
		})
	}

	return rv, nil
}
