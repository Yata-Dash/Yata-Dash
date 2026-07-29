package main

import (
	"reflect"
	"testing"
)

// --allowed-hosts / YATA_ALLOWED_HOSTS is the deployment-level half of the
// host allow-list; the other half lives in Settings → Network and is read
// live by the server (see internal/api/hostguard.go, allowedHostsFor).
func TestSplitHosts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"unset", "", nil},
		{"whitespace only", "   ", nil},
		{"one", "yata.example.com", []string{"yata.example.com"}},
		{"several with spacing", "a.example, b.example ,c.example",
			[]string{"a.example", "b.example", "c.example"}},
		{"trailing comma does not create a blank host", "a.example,", []string{"a.example"}},
		{"interior blanks dropped", "a.example,,b.example", []string{"a.example", "b.example"}},
		{"wildcard passes through — only settable here, not from the UI", "*", []string{"*"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := splitHosts(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("splitHosts(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
