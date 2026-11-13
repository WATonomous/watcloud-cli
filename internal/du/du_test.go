//go:build linux

package du

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"
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

func findEntry(t *testing.T, view directory, name string) entry {
	t.Helper()
	for _, item := range view.entries {
		if item.name == name {
			return item
		}
	}
	t.Fatalf("entry %q not found", name)
	return entry{}
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

func TestQuotaStatesAndInvalidNumbers(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  uint64
		bad   bool
	}{
		{value: "max_bytes=21474836480 max_files=0", want: 21474836480},
		{value: " max_files=0 max_bytes=1000\n", want: 1000},
		{value: "max_bytes=0 max_files=0"},
		{value: "max_files=0", bad: true},
		{value: "max_bytes=invalid", bad: true},
		{value: "max_bytes=-1", bad: true},
		{value: "max_bytes=18446744073709551616", bad: true},
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
	unavailable := errors.New("permission denied")
	got := readQuota(func(string, string) (string, error) {
		return "", unavailable
	}, "")
	if !errors.Is(got.err, unavailable) {
		t.Fatalf("unreadable quota became unlimited: %+v", got)
	}
}

func TestMetadataErrorsRemainUnknownAndRefreshable(t *testing.T) {
	home, sizes := fixture(t)
	broken := true
	unavailable := errors.New("metadata unavailable")
	attr := attributes(sizes)
	fs := testFilesystem(t, home, func(path, name string) (string, error) {
		if broken && path == filepath.Join(home, ".cache") {
			return "", unavailable
		}
		return attr(path, name)
	})
	view, err := fs.load(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	item := findEntry(t, view, ".cache")
	if !errors.Is(item.size.err, unavailable) || sizeText(item.size) != "?" {
		t.Fatalf("failed metadata did not remain unknown: %+v", item)
	}
	if view.entries[len(view.entries)-1].name != ".cache" {
		t.Fatal("unknown size should sort last")
	}
	broken = false
	view, err = fs.load(context.Background(), ".")
	if err != nil || findEntry(t, view, ".cache").size.bytes != 900 {
		t.Fatalf("refresh failed: %v", err)
	}
}

func TestSymlinksSpecialFilesAndRemovedEntries(t *testing.T) {
	home, sizes := fixture(t)
	outside := t.TempDir()
	for name, target := range map[string]string{"outside-link": outside, "broken-link": "/not-present", "loop": "loop"} {
		if err := os.Symlink(target, filepath.Join(home, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(home, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	socket, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(socket)
	if err := unix.Bind(socket, &unix.SockaddrUnix{Name: filepath.Join(home, "socket")}); err != nil {
		t.Fatal(err)
	}
	fs := testFilesystem(t, home, attributes(sizes))
	view, err := fs.load(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outside-link", "broken-link", "loop"} {
		item := findEntry(t, view, name)
		info, _ := os.Lstat(filepath.Join(home, name))
		if item.kind != "symlink" || item.navigable() || item.size.bytes != uint64(info.Size()) {
			t.Fatalf("symlink followed or mismeasured: %+v", item)
		}
	}
	for _, name := range []string{"pipe", "socket"} {
		item := findEntry(t, view, name)
		if item.kind != map[string]string{"pipe": "fifo", "socket": "socket"}[name] || item.size.err == nil || item.navigable() {
			t.Fatalf("special file misclassified: %+v", item)
		}
	}
	if err := os.Remove(filepath.Join(home, "file")); err != nil {
		t.Fatal(err)
	}
	removed := fs.inspect(fs.home, "file")
	if !errors.Is(removed.size.err, os.ErrNotExist) || sizeText(removed.size) != "?" {
		t.Fatalf("removed entry not tolerated: %+v", removed)
	}
	view, err = fs.load(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.entries {
		if item.name == "file" {
			t.Fatal("removed file survived refresh")
		}
	}
}

func TestOtherFilesystemsAreExcluded(t *testing.T) {
	fs := testFilesystem(t, "/", attributes(nil))
	proc := fs.inspect(fs.home, "proc")
	if proc.navigable() || proc.size.err == nil || !strings.Contains(proc.note, "other filesystem") {
		t.Fatalf("/proc was not excluded: %+v", proc)
	}
	if _, err := fs.load(context.Background(), "proc"); err == nil {
		t.Fatal("other filesystem loaded")
	}
}

func readyBrowser(t *testing.T, fs *filesystem) browser {
	t.Helper()
	m := newBrowser(context.Background(), fs)
	m.colored = false
	next, _ := m.Update(m.Init()())
	m = next.(browser)
	if m.fatal != nil {
		t.Fatal(m.fatal)
	}
	return m
}

func step(m browser, msg tea.Msg) browser {
	next, cmd := m.Update(msg)
	if cmd != nil {
		next, _ = next.Update(cmd())
	}
	return next.(browser)
}

func refresh(m browser) browser {
	return step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
}

func TestFilenameEscapingAndRawNavigation(t *testing.T) {
	home := t.TempDir()
	name := strings.Repeat("a", 80) + "\n\x1b[31m\u202e\xff"
	if err := os.Mkdir(filepath.Join(home, name), 0700); err != nil {
		t.Fatal(err)
	}
	fs := testFilesystem(t, home, attributes(map[string]string{home: "100", filepath.Join(home, name): "100"}))
	m := readyBrowser(t, fs)
	text := m.View()
	if strings.ContainsAny(text, "\x1b\u202e") || strings.Contains(text, name) || !strings.Contains(text, "…") {
		t.Fatalf("unsafe or untruncated filename rendered: %q", text)
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view.path != name {
		t.Fatal("truncation corrupted navigation")
	}
	if got := safeText("x\n\t\x1b\u202e\xff"); !strings.Contains(got, `\n\t\x1b\u202e\xff`) {
		t.Fatalf("controls were not escaped: %q", got)
	}
}

func TestRenderUnavailableQuotaAndUsage(t *testing.T) {
	unavailable := errors.New("permission denied")
	m := browser{width: 80, height: 24,
		view: directory{path: ".", size: measurement{bytes: 100}}, homeUsed: measurement{bytes: 100}}
	for _, quota := range []measurement{{err: unavailable}, {bytes: 0}} {
		m.quota = quota
		text := m.View()
		if strings.Contains(text, "%") {
			t.Fatalf("percentage shown without a denominator: %s", text)
		}
		label := "no storage limit"
		if quota.err != nil {
			label = "limit unavailable"
		}
		if !strings.Contains(text, label) {
			t.Fatalf("quota states not distinguished: %s", text)
		}
	}
	m.view.size, m.homeUsed, m.quota = measurement{err: unavailable}, measurement{err: unavailable}, measurement{bytes: 100}
	if text := m.View(); strings.Contains(text, "Home: 0 B") || strings.Contains(text, "This folder: 0 B") || strings.Contains(text, "0.0%") {
		t.Fatalf("unknown usage became zero: %s", text)
	}
}

func TestBrowseNavigationAndHomePercentages(t *testing.T) {
	home, sizes := fixture(t)
	fs := testFilesystem(t, home, attributes(sizes))
	m := readyBrowser(t, fs)
	m = step(m, tea.KeyMsg{Type: tea.KeyDown})
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view.path != "." {
		t.Fatal("a file was treated as a folder")
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyUp})
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view.path != ".cache" || !strings.Contains(m.View(), "1.2%") {
		t.Fatalf("navigation or home percentage incorrect:\n%s", m.View())
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view.path != filepath.Join(".cache", "sub") {
		t.Fatal("could not enter a nested folder")
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.view.path != ".cache" || m.selected().name != "sub" {
		t.Fatal("going back lost the nested selection")
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.view.path != "." || m.view.entries[m.cursor].name != ".cache" {
		t.Fatal("going back did not restore selection")
	}
	// A newly larger item changes row numbers; refresh must keep the selected name.
	larger := filepath.Join(home, "larger")
	if err := os.Mkdir(larger, 0700); err != nil {
		t.Fatal(err)
	}
	sizes[larger] = "2000"
	m = refresh(m)
	if m.view.entries[m.cursor].name != ".cache" || !strings.Contains(m.View(), "9.0%") {
		t.Fatalf("refresh lost selection or the home denominator:\n%s", m.View())
	}
}

func TestBrowseKeepsParentWhenSelectedDirectoryDisappears(t *testing.T) {
	home, sizes := fixture(t)
	fs := testFilesystem(t, home, attributes(sizes))
	m := readyBrowser(t, fs)
	if err := os.Rename(filepath.Join(home, ".cache"), filepath.Join(home, "old")); err != nil {
		t.Fatal(err)
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.fatal != nil || m.view.path != "." || m.status == "" || len(m.parents) != 0 {
		t.Fatalf("navigation failure was fatal or left the parent: %+v", m)
	}
	if !strings.Contains(m.View(), unix.ENOENT.Error()) {
		t.Fatalf("missing folder cause not visible:\n%s", m.View())
	}
	m = refresh(m)
	if m.status != "" || m.view.path == "" {
		t.Fatal("could not recover by refreshing")
	}
}

func TestFailedBackPopsDeletedParents(t *testing.T) {
	home, sizes := fixture(t)
	m := readyBrowser(t, testFilesystem(t, home, attributes(sizes)))
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if err := os.RemoveAll(filepath.Join(home, ".cache")); err != nil {
		t.Fatal(err)
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.view.path != filepath.Join(".cache", "sub") || len(m.parents) != 1 || m.status == "" || m.loading {
		t.Fatalf("failed Back did not pop the deleted parent: %+v", m)
	}
	if !strings.Contains(m.View(), unix.ENOENT.Error()) {
		t.Fatalf("failed Back cause not visible:\n%s", m.View())
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.view.path != "." || len(m.parents) != 0 || m.status != "" {
		t.Fatalf("Back did not escape to home: %+v", m)
	}
}

func TestBrowseRecreatedFolders(t *testing.T) {
	for _, action := range []string{"open", "refresh", "back"} {
		t.Run(action, func(t *testing.T) {
			home, sizes := fixture(t)
			m := readyBrowser(t, testFilesystem(t, home, attributes(sizes)))
			if action != "open" {
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			if action == "back" {
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			cache := filepath.Join(home, ".cache")
			// Keep the removed inode alive so the recreated folder cannot reuse it.
			old, err := os.Open(cache)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := os.RemoveAll(cache); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(cache, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cache, "new-file"), []byte("rebuilt"), 0600); err != nil {
				t.Fatal(err)
			}
			sizes[cache] = "7"
			switch action {
			case "open":
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			case "refresh":
				m = refresh(m)
			case "back":
				m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
			}
			if m.view.path != ".cache" || m.status != "" || m.view.size.bytes != 7 || len(m.view.entries) != 1 || m.selected().name != "new-file" {
				t.Fatalf("%s did not load the recreated folder: %+v", action, m)
			}
		})
	}
}

func TestBrowseShowsPermissionErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories regardless of permission bits")
	}
	for _, action := range []string{"open", "refresh", "back"} {
		t.Run(action, func(t *testing.T) {
			home, sizes := fixture(t)
			m := readyBrowser(t, testFilesystem(t, home, attributes(sizes)))
			if action != "open" {
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			if action == "back" {
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			previous := m.view.path
			cache := filepath.Join(home, ".cache")
			if err := os.Chmod(cache, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(cache, 0700) })
			switch action {
			case "open":
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			case "refresh":
				m = refresh(m)
			case "back":
				m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
			}
			if m.view.path != previous || m.loading || m.fatal != nil {
				t.Fatalf("failed %s changed the view or left the browser loading: %+v", action, m)
			}
			if text := m.View(); !strings.Contains(text, unix.EACCES.Error()) || strings.Contains(text, "try again") {
				t.Fatalf("failed %s did not show the permission error:\n%s", action, text)
			}
		})
	}
}

func TestBrowseEscapesLoadErrors(t *testing.T) {
	m := browser{width: 80, height: 24, view: directory{path: "."}, loading: true}
	m = step(m, loaded{err: errors.New("unavailable\n\x1b[31m")})
	if text := m.View(); !strings.Contains(text, `unavailable\n\x1b[31m`) || strings.Contains(text, "\x1b") {
		t.Fatalf("load error was lost or rendered control characters: %q", text)
	}
}

func TestBrowseShowsReplacedFileError(t *testing.T) {
	home, sizes := fixture(t)
	m := readyBrowser(t, testFilesystem(t, home, attributes(sizes)))
	cache := filepath.Join(home, ".cache")
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, nil, 0600); err != nil {
		t.Fatal(err)
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view.path != "." || !strings.Contains(m.View(), unix.ENOTDIR.Error()) {
		t.Fatalf("replacement file cause not visible:\n%s", m.View())
	}
}

func TestQuotaFailureDoesNotPreventBrowsingAndRefreshRecovers(t *testing.T) {
	home := t.TempDir()
	quotaReads := 0
	fs := testFilesystem(t, home, func(_, name string) (string, error) {
		if name != "ceph.quota" {
			return "500", nil
		}
		quotaReads++
		switch quotaReads {
		case 1:
			return "", os.ErrPermission
		case 2:
			return "max_bytes=0", nil
		default:
			return "max_bytes=1000", nil
		}
	})
	m := readyBrowser(t, fs)
	for _, want := range []string{"limit unavailable", "no storage limit", "50.0%"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("missing quota state %q:\n%s", want, m.View())
		}
		m = refresh(m)
	}
}

func TestViewportResizeAndUnicodeWidth(t *testing.T) {
	m := browser{width: 60, height: 12, view: directory{path: "."}}
	for i := 0; i < 30; i++ {
		m.view.entries = append(m.view.entries, entry{name: fmt.Sprintf("%02d-%s", i, strings.Repeat("界", 40)), kind: "file"})
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != 29 || m.offset == 0 {
		t.Fatal("selection did not scroll into view")
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("line exceeds terminal width: %q", line)
		}
	}
	if len(strings.Split(m.View(), "\n")) > m.height {
		t.Fatal("view exceeds terminal height")
	}
	m = step(m, tea.WindowSizeMsg{Width: 35, Height: 10})
	if !strings.Contains(m.View(), "larger") || !strings.Contains(m.View(), "q Quit") {
		t.Fatal("small terminal has no useful guidance")
	}
}

func TestQuitRemainsAvailableWhileLoading(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC},
	} {
		m := browser{loading: true}
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatal("quit ignored while loading")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("quit key did not produce a quit command")
		}
	}
}

func TestNoColorAndCanceledQueuedLoad(t *testing.T) {
	home, sizes := fixture(t)
	fs := testFilesystem(t, home, attributes(sizes))
	t.Setenv("NO_COLOR", "1")
	ctx, cancel := context.WithCancel(context.Background())
	m := newBrowser(ctx, fs)
	if m.colored {
		t.Fatal("NO_COLOR ignored")
	}
	cmd := m.Init()
	cancel()
	if result := cmd().(loaded); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("queued load ignored cancellation: %v", result.err)
	}
}

func TestUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories regardless of permission bits")
	}
	home, sizes := fixture(t)
	fs := testFilesystem(t, home, attributes(sizes))
	cache := filepath.Join(home, ".cache")
	if err := os.Chmod(cache, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cache, 0700) })
	if _, err := fs.load(context.Background(), ".cache"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unreadable directory: %v", err)
	}
}

func TestUnsupportedFilesystemAndRedirection(t *testing.T) {
	if _, err := openStorage("/proc", attributes(nil)); err == nil {
		t.Fatal("non-Ceph filesystem accepted")
	} else if !strings.Contains(err.Error(), "CephFS") {
		t.Fatalf("unclear filesystem error: %v", err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("q\n"), &out); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("redirection accepted: %v", err)
	}
}

func TestAttributeReadsCurrentPathWithoutFollowingSymlinks(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Native reads need no external tools.
	path := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(path, []byte("123"), 0600); err != nil {
		t.Fatal(err)
	}
	const attribute = "user.watcloud-test"
	if err := unix.Setxattr(path, attribute, []byte("123"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("temporary filesystem does not support extended attributes")
		}
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("456"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, attribute, []byte("456"), 0); err != nil {
		t.Fatal(err)
	}
	got, err := getAttribute(path, attribute)
	if err != nil || got != "456" {
		t.Fatalf("attribute reader did not read the current pathname: %q, %v", got, err)
	}
	if _, err := getAttribute(path, "user.missing"); !errors.Is(err, unix.ENODATA) {
		t.Fatalf("missing attribute error swallowed: %v", err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if got, err := getAttribute(link, attribute); err == nil {
		t.Fatalf("attribute reader followed symlink: %q", got)
	}
}
