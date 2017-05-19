package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDetailParsing(t *testing.T) {
	tests := []struct {
		in  string
		exp map[string]string
	}{
		{"", map[string]string{}},
		{"\n", map[string]string{}},
		{"test=true", map[string]string{"test": "true"}},
		{"test=true nottest=false", map[string]string{
			"test":    "true",
			"nottest": "false",
		}},
	}

	for _, test := range tests {
		got := parseMap(strings.NewReader(test.in))
		if !reflect.DeepEqual(got, test.exp) {
			t.Errorf("parseMap(%q) = %v, want %v", test.in, got, test.exp)
		}
	}
}

func TestSnapshotParsing(t *testing.T) {
	fn := "26-20170518102400-snapshot.jpg"
	ts, err := parseSnapshotTime(fn)
	if err != nil {
		t.Fatalf("Failure parsing: %v", err)
	}
	got := ts.Format(time.RFC3339)
	want := "2017-05-18T10:24:00-07:00"
	if got != want {
		t.Errorf("parse(%v) = %v, want %v", fn, got, want)
	}
}
