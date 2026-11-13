//go:build linux

package du

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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

func TestBrowseNavigation(t *testing.T) {
	home, sizes := fixture(t)
	m := readyBrowser(t, testFilesystem(t, home, attributes(sizes)))
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view.path != ".cache" || !strings.Contains(m.View(), "9.0%") {
		t.Fatalf("folder or home percentage incorrect:\n%s", m.View())
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
	if m.view.path != "." || m.selected().name != ".cache" {
		t.Fatal("going back did not restore selection")
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
	if !strings.Contains(m.View(), "no such file or directory") {
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
	if !strings.Contains(m.View(), "no such file or directory") {
		t.Fatalf("failed Back cause not visible:\n%s", m.View())
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.view.path != "." || len(m.parents) != 0 || m.status != "" {
		t.Fatalf("Back did not escape to home: %+v", m)
	}
}

func TestBrowseRecreatedFolders(t *testing.T) {
	for _, action := range []string{"refresh", "back"} {
		t.Run(action, func(t *testing.T) {
			home, sizes := fixture(t)
			m := readyBrowser(t, testFilesystem(t, home, attributes(sizes)))
			m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			if action == "back" {
				m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			cache := filepath.Join(home, ".cache")
			// Prevent inode reuse so this catches the original identity-check bug.
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
			sizes[cache] = "7"
			if action == "refresh" {
				m = refresh(m)
			} else {
				m = step(m, tea.KeyMsg{Type: tea.KeyLeft})
			}
			if m.view.path != ".cache" || m.status != "" || m.view.size.bytes != 7 || len(m.view.entries) != 0 {
				t.Fatalf("%s did not load the recreated folder: %+v", action, m)
			}
		})
	}
}

func TestBrowseShowsLoadError(t *testing.T) {
	m := browser{width: 80, height: 24, view: directory{path: "."}, loading: true}
	m = step(m, loaded{err: &os.PathError{
		Op: "open", Path: strings.Repeat("long-folder/", 10), Err: os.ErrPermission,
	}})
	if m.view.path != "." || m.loading || !strings.Contains(m.View(), "permission denied") {
		t.Fatalf("load failure lost the view or hid the cause:\n%s", m.View())
	}
}
