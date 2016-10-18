package scenic

import (
	"reflect"
	"time"

	"golang.org/x/net/context"

	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
)

type Camera struct {
	Name  string `json:"name" datastore:"name"`
	Token string `json:"token" datastore:"token"`

	Key *datastore.Key `datastore:"-"`
}

func (u *Camera) setKey(to *datastore.Key) {
	u.Key = to
}

type Event struct {
	Camera    *datastore.Key `json:"cam_id" datastore:"camera"`
	Timestamp time.Time      `json:"ts" datastore:"ts"`
	Filename  string         `json:"fn" datastore:"fn"`
	Duration  time.Duration  `json:"duration" datastore"duration"`

	Key *datastore.Key `datastore:"-"`
}

func (u *Event) setKey(to *datastore.Key) {
	u.Key = to
}

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
