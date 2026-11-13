//go:build linux

package du

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Run with TMPDIR on a local filesystem to measure entry handling without
// writing file contents or generating a large metadata workload on Ceph.
func BenchmarkLoadLargeDirectory(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			home := b.TempDir()
			for i := 0; i < count; i++ {
				file, err := os.Create(filepath.Join(home, fmt.Sprintf("file-%06d", i)))
				if err != nil {
					b.Fatal(err)
				}
				if err := file.Close(); err != nil {
					b.Fatal(err)
				}
			}
			reads := 0
			fs, err := newFilesystem(home, func(string, string) (string, error) {
				reads++
				return "0", nil
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				view, err := fs.load(context.Background(), ".")
				if err != nil || len(view.entries) != count {
					b.Fatalf("load returned %d entries: %v", len(view.entries), err)
				}
			}
			b.StopTimer()
			if reads != b.N {
				b.Fatalf("read %d attributes; want only one directory total per load", reads)
			}
		})
	}
}

func BenchmarkViewLargeDirectory(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := browser{width: 80, height: 24,
				view: directory{path: "."}, quota: measurement{bytes: 1 << 40}}
			for i := 0; i < count; i++ {
				m.view.entries = append(m.view.entries, entry{
					name: fmt.Sprintf("file-%06d", i), kind: "file", size: measurement{bytes: uint64(i)},
				})
			}
			m.cursor, m.offset = count-1, count-m.rows()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.View()
			}
		})
	}
}
