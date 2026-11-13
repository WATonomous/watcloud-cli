//go:build linux

package du

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// Read the same Ceph virtual attributes as getfattr, without spawning a process.
func getAttribute(path, name string) (string, error) {
	// Both attributes contain numeric values; 256 bytes covers their uint64 fields.
	var value [256]byte
	n, err := unix.Lgetxattr(path, name, value[:])
	if err != nil {
		return "", fmt.Errorf("read %s on %q: %w", name, path, err)
	}
	return strings.TrimSpace(string(value[:n])), nil
}

func openStorage(home string, attribute attributeReader) (*filesystem, error) {
	fs, err := newFilesystem(home, attribute)
	if err != nil {
		return nil, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(fs.home, &stat); err != nil {
		return nil, err
	}
	if stat.Type != unix.CEPH_SUPER_MAGIC {
		return nil, fmt.Errorf("watcloud du requires a kernel-mounted CephFS home; %q is on filesystem type %#x", home, stat.Type)
	}
	return fs, nil
}

// newFilesystem leaves filesystem-type validation to openStorage so tests can
// exercise directory loading on a temporary filesystem.
func newFilesystem(home string, attribute attributeReader) (*filesystem, error) {
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(home)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("home %q is not a directory", home)
	}
	return &filesystem{home: home, device: uint64(info.Sys().(*syscall.Stat_t).Dev), attribute: attribute}, nil
}

func (fs *filesystem) checkDevice(info os.FileInfo) error {
	if uint64(info.Sys().(*syscall.Stat_t).Dev) != fs.device {
		return errors.New("other filesystem (excluded from home quota)")
	}
	return nil
}

func (fs *filesystem) load(ctx context.Context, relative string) (directory, error) {
	if err := ctx.Err(); err != nil {
		return directory{}, err
	}
	path := filepath.Join(fs.home, relative)
	info, err := os.Lstat(path)
	if err != nil {
		return directory{}, err
	}
	if !info.IsDir() {
		return directory{}, &os.PathError{Op: "open", Path: path, Err: unix.ENOTDIR}
	}
	if err := fs.checkDevice(info); err != nil {
		return directory{}, &os.PathError{Op: "open", Path: path, Err: err}
	}
	items, err := os.ReadDir(path)
	if err != nil && len(items) == 0 {
		return directory{}, fmt.Errorf("list %q: %w", path, err)
	}
	view := directory{path: filepath.Clean(relative), size: readSize(fs.attribute, path), warning: err}
	for _, item := range items {
		view.entries = append(view.entries, fs.inspect(path, item.Name()))
	}
	sortEntries(view.entries)
	return view, nil
}

func (fs *filesystem) inspect(parent, name string) entry {
	item := entry{name: name, kind: "unknown"}
	path := filepath.Join(parent, name)
	info, err := os.Lstat(path)
	if err != nil {
		item.size.err = err
		return item
	}
	switch mode := info.Mode(); {
	case mode.IsDir():
		item.kind = "directory"
	case mode.IsRegular():
		item.kind = "file"
	case mode&os.ModeSymlink != 0:
		item.kind = "symlink"
	case mode&os.ModeNamedPipe != 0:
		item.kind = "fifo"
	case mode&os.ModeSocket != 0:
		item.kind = "socket"
	case mode&os.ModeCharDevice != 0:
		item.kind = "char device"
	case mode&os.ModeDevice != 0:
		item.kind = "block device"
	}
	if err := fs.checkDevice(info); err != nil {
		item.note = err.Error()
		item.size.err = err
		return item
	}
	switch item.kind {
	case "directory":
		item.size = readSize(fs.attribute, path)
	case "file", "symlink":
		item.size.bytes = uint64(info.Size())
	default:
		item.note = "special file; contents not opened"
		item.size.err = errors.New("not a quota-byte measurement")
	}
	return item
}

// Reject redirected input/output before starting the interactive UI.
func requireTerminal(in io.Reader, out io.Writer) error {
	for _, stream := range []any{in, out} {
		file, ok := stream.(term.File)
		if !ok || !term.IsTerminal(file.Fd()) {
			return errors.New("watcloud du requires an interactive terminal on stdin and stdout")
		}
	}
	return nil
}
