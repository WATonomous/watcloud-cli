package du

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type attributeReader func(path, name string) (string, error)

// A failed measurement is distinct from a known zero.
type measurement struct {
	bytes uint64
	err   error
}

type entry struct {
	name string // Raw filesystem name; escaping and truncation are display-only.
	kind string
	size measurement
	note string
}

func (e entry) navigable() bool { return e.kind == "directory" && e.note == "" }

type directory struct {
	path    string // Relative to the home directory.
	size    measurement
	entries []entry
	warning error
}

type filesystem struct {
	home      string
	device    uint64
	attribute attributeReader
}

func (fs *filesystem) quota() measurement {
	return readQuota(fs.attribute, fs.home)
}
func (fs *filesystem) homeUsage() measurement {
	return readSize(fs.attribute, fs.home)
}

func readQuota(attribute attributeReader, home string) measurement {
	value, err := attribute(home, "ceph.quota")
	if err != nil {
		return measurement{err: err}
	}
	for _, field := range strings.Fields(value) {
		if raw, ok := strings.CutPrefix(field, "max_bytes="); ok {
			quota, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return measurement{err: fmt.Errorf("invalid home quota %q: %w", raw, err)}
			}
			return measurement{bytes: quota}
		}
	}
	return measurement{err: errors.New("home quota has no max_bytes value")}
}

func readSize(attribute attributeReader, path string) measurement {
	value, err := attribute(path, "ceph.dir.rbytes")
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
