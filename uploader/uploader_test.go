package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestDetailParsing(t *testing.T) {
	tests := []struct {
		in  string
		exp map[string]string
	}{
		{"", map[string]string{}},
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
