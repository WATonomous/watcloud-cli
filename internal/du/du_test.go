//go:build linux

package du

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	cache := filepath.Join(home, ".cache")
	sub := filepath.Join(cache, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, "file"), filepath.Join(home, "another"), filepath.Join(sub, "deep-file")} {
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home, map[string]string{home: "1000", cache: "900", sub: "123"}
}

func testFilesystem(t *testing.T, home string, attr attributeReader) *filesystem {
	t.Helper()
	fs, err := newFilesystem(home, attr)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func attributes(sizes map[string]string) attributeReader {
	return func(path, name string) (string, error) {
		if name == "ceph.quota" {
			return "max_bytes=10000 max_files=0", nil
		}
		value, ok := sizes[path]
		if !ok {
			return "", errors.New("unexpected attribute path")
		}
		return value, nil
	}
}

func TestLoadUsesImmediateMetadataAndSorts(t *testing.T) {
	home, sizes := fixture(t)
	var calls []string
	attr := attributes(sizes)
	fs := testFilesystem(t, home, func(path, name string) (string, error) {
		calls = append(calls, path)
		return attr(path, name)
	})
	view, err := fs.load(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, item := range view.entries {
		names = append(names, item.name)
	}
	if view.size.bytes != 1000 || !reflect.DeepEqual(names, []string{".cache", "another", "file"}) {
		t.Fatalf("unexpected directory: %+v", view)
	}
	if want := []string{home, filepath.Join(home, ".cache")}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("attribute calls = %v, want %v", calls, want)
	}
}

func TestQuotaStates(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  uint64
		bad   bool
	}{
		{value: "max_bytes=1000 max_files=0", want: 1000},
		{value: "max_bytes=0 max_files=0"},
		{value: "max_files=0", bad: true},
		{value: "max_bytes=invalid", bad: true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got := readQuota(func(string, string) (string, error) {
				return tc.value, nil
			}, "")
			if (got.err != nil) != tc.bad || got.bytes != tc.want {
				t.Fatalf("quota = %+v; want %d, error=%t", got, tc.want, tc.bad)
			}
		})
	}
}
