package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTTLAndPermissions(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := Store{Dir: t.TempDir(), Now: func() time.Time { return now }}
	if err := s.Put("query", []byte("value")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Dir, "go-"+Key("query"))
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	for _, tc := range []struct {
		age time.Duration
		hit bool
	}{{TTL, true}, {TTL + time.Second, false}, {-time.Hour, true}} {
		when := now.Add(-tc.age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		_, hit := s.Get("query")
		if hit != tc.hit {
			t.Fatalf("age %v: %v", tc.age, hit)
		}
	}
}
