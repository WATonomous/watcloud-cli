package du

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type attributeReader func(context.Context, *os.File, string) (string, error)

// A failed measurement is distinct from a known zero.
type measurement struct {
	bytes uint64
	err   error
}

type identity struct {
	device uint64
	inode  uint64
}

type entry struct {
	name string // Raw filesystem name; escaping and truncation are display-only.
	kind string
	size measurement
	id   identity
	note string
}

func (e entry) navigable() bool { return e.kind == "directory" && e.note == "" }

type directory struct {
	path    string // Relative to the pinned home directory.
	id      identity
	size    measurement
	entries []entry
	warning error
}

type filesystem struct {
	root      *os.File
	mount     uint64
	attribute attributeReader
	mountID   func(*os.File) (uint64, error)
}

func (fs *filesystem) rootPath() string { return fs.root.Name() }
func (fs *filesystem) Close() error     { return fs.root.Close() }
func (fs *filesystem) quota(ctx context.Context) measurement {
	return readQuota(ctx, fs.attribute, fs.root)
}
func (fs *filesystem) homeUsage(ctx context.Context) measurement {
	return readSize(ctx, fs.attribute, fs.root)
}

func readQuota(ctx context.Context, attribute attributeReader, home *os.File) measurement {
	value, err := attribute(ctx, home, "ceph.quota")
	if err != nil {
		return measurement{err: err}
	}
	var quota uint64
	found := false
	for _, field := range strings.Fields(value) {
		if raw, ok := strings.CutPrefix(field, "max_bytes="); ok {
			if found {
				return measurement{err: errors.New("home quota has duplicate max_bytes values")}
			}
			found = true
			quota, err = strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return measurement{err: fmt.Errorf("invalid home quota %q: %w", raw, err)}
			}
		}
	}
	if found {
		return measurement{bytes: quota}
	}
	return measurement{err: errors.New("home quota has no max_bytes value")}
}

func readSize(ctx context.Context, attribute attributeReader, file *os.File) measurement {
	value, err := attribute(ctx, file, "ceph.dir.rbytes")
	if err != nil {
		return measurement{err: err}
	}
	size, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return measurement{err: fmt.Errorf("invalid CephFS size %q: %w", value, err)}
	}
	return measurement{bytes: size}
}

func sortEntries(entries []entry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.size.err == nil) != (b.size.err == nil) {
			return a.size.err == nil // Unknown sizes sort after known sizes.
		}
		if a.size.bytes != b.size.bytes {
			return a.size.bytes > b.size.bytes
		}
		return a.name < b.name
	})
}
