//go:build linux

package du

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// Read the same Ceph virtual attributes as getfattr, without spawning a process.
// The descriptor path also works for O_PATH handles and pins the original inode.
func getAttribute(ctx context.Context, file *os.File, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Both attributes contain numeric values; 256 bytes covers their uint64 fields.
	var value [256]byte
	n, err := unix.Getxattr(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), name, value[:])
	runtime.KeepAlive(file)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("read %s on %q: %w", name, file.Name(), err)
	}
	return strings.TrimSpace(string(value[:n])), nil
}

// All paths after startup are resolved relative to this pinned home descriptor.
// O_NOFOLLOW applies at every component; mount IDs also reject same-device bind
// mounts. O_PATH lets us inspect links, FIFOs and devices without opening content.
func openStorage(home string, attribute attributeReader) (*filesystem, error) {
	fs, err := newFilesystem(home, attribute)
	if err != nil {
		return nil, err
	}
	var stat unix.Statfs_t
	err = unix.Fstatfs(int(fs.root.Fd()), &stat)
	if err == nil && stat.Type != unix.CEPH_SUPER_MAGIC {
		err = fmt.Errorf("watcloud du requires a kernel-mounted CephFS home; %q is on filesystem type %#x", home, stat.Type)
	}
	if err != nil {
		fs.Close()
		return nil, err
	}
	return fs, nil
}

// newFilesystem leaves filesystem-type validation to openStorage so tests can
// exercise the actual descriptor and mount boundaries on a temporary filesystem.
func newFilesystem(home string, attribute attributeReader) (*filesystem, error) {
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open home %q: %w", home, err)
	}
	root := os.NewFile(uintptr(fd), home)
	mount, err := descriptorMountID(root)
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("cannot enforce mount boundaries: %w", err)
	}
	return &filesystem{root: root, mount: mount, attribute: attribute, mountID: descriptorMountID}, nil
}

func descriptorMountID(file *os.File) (uint64, error) {
	// Query the pinned inode directly. This avoids opening and parsing a proc
	// file for every entry; older kernels still use the same mount-ID fallback.
	var stat unix.Statx_t
	err := unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW|unix.AT_STATX_DONT_SYNC,
		unix.STATX_MNT_ID, &stat)
	runtime.KeepAlive(file)
	if err == nil && stat.Mask&unix.STATX_MNT_ID != 0 {
		return stat.Mnt_id, nil
	}
	return procMountID(file)
}

func procMountID(file *os.File) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", file.Fd()))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if raw, ok := strings.CutPrefix(line, "mnt_id:"); ok {
			return strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		}
	}
	return 0, errors.New("mount ID unavailable in /proc/self/fdinfo")
}

func (fs *filesystem) checkMount(file *os.File) error {
	mount, err := fs.mountID(file)
	if err != nil {
		return fmt.Errorf("cannot verify mount: %w", err)
	}
	if mount != fs.mount {
		return errors.New("other mount (excluded from home quota)")
	}
	return nil
}

func fileIdentity(file *os.File) (identity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return identity{}, err
	}
	return identity{device: uint64(stat.Dev), inode: stat.Ino}, nil
}

func (fs *filesystem) openDirectory(relative string) (*os.File, error) {
	if !filepath.IsLocal(relative) {
		return nil, fmt.Errorf("path %q is outside home", relative)
	}
	relative = filepath.Clean(relative)
	fd, err := unix.Openat(int(fs.root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), fs.rootPath())
	if relative != "." {
		for _, part := range strings.Split(relative, string(os.PathSeparator)) {
			fd, err := unix.Openat(int(file.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			name := filepath.Join(file.Name(), part)
			file.Close()
			if err != nil {
				return nil, fmt.Errorf("open directory %q without following symlinks: %w", name, err)
			}
			file = os.NewFile(uintptr(fd), name)
			if err := fs.checkMount(file); err != nil {
				file.Close()
				return nil, fmt.Errorf("%q: %w", name, err)
			}
		}
	}
	return file, nil
}

func (fs *filesystem) load(ctx context.Context, relative string, expected *identity) (directory, error) {
	if err := ctx.Err(); err != nil {
		return directory{}, err
	}
	file, err := fs.openDirectory(relative)
	if err != nil {
		return directory{}, err
	}
	defer file.Close()
	id, err := fileIdentity(file)
	if err != nil {
		return directory{}, err
	}
	if expected != nil && id != *expected {
		return directory{}, errors.New("directory changed since it was listed; refresh its parent")
	}
	view := directory{path: filepath.Clean(relative), id: id, size: readSize(ctx, fs.attribute, file)}
	// Read bounded batches so large flat directories do not need a second
	// full list in memory and cancellation is checked between batches.
	for {
		if err := ctx.Err(); err != nil {
			return directory{}, err
		}
		names, err := file.Readdirnames(128)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return directory{}, err
			}
			view.entries = append(view.entries, fs.inspect(ctx, file, name))
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(view.entries) == 0 {
				return directory{}, fmt.Errorf("list %q: %w", file.Name(), err)
			}
			view.warning = err
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return directory{}, err
	}
	sortEntries(view.entries)
	if err := ctx.Err(); err != nil {
		return directory{}, err
	}
	return view, nil
}

func (fs *filesystem) inspect(ctx context.Context, parent *os.File, name string) entry {
	item := entry{name: name, kind: "unknown"}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		item.size.err = err
		return item
	}
	file := os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name))
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		item.size.err = err
		return item
	}
	item.id = identity{device: uint64(stat.Dev), inode: stat.Ino}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		item.kind = "directory"
	case unix.S_IFREG:
		item.kind = "file"
	case unix.S_IFLNK:
		item.kind = "symlink"
	case unix.S_IFIFO:
		item.kind = "fifo"
	case unix.S_IFSOCK:
		item.kind = "socket"
	case unix.S_IFCHR:
		item.kind = "char device"
	case unix.S_IFBLK:
		item.kind = "block device"
	}
	if err := fs.checkMount(file); err != nil {
		item.note = err.Error()
		item.size.err = err
		return item
	}
	switch item.kind {
	case "directory":
		item.size = readSize(ctx, fs.attribute, file)
	case "file", "symlink":
		if stat.Size < 0 {
			item.size.err = errors.New("invalid negative file size")
		} else {
			item.size.bytes = uint64(stat.Size)
		}
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
