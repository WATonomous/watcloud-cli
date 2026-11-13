//go:build linux

package du

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 80}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func TestStalledReadDoesNotDelayExit(t *testing.T) {
	for _, key := range []string{"q", "\x1b", "\x03", "cancel"} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			master, slave := testTerminal(t)
			before, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			output := make(chan []byte, 1)
			go func() {
				data, _ := io.ReadAll(master)
				output <- data
			}()
			started, release := make(chan struct{}), make(chan struct{})
			signalStarted := sync.OnceFunc(func() { close(started) })
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			fs, err := newFilesystem(t.TempDir(), func(string, string) (string, error) {
				signalStarted()
				<-release // Deliberately ignores cancellation, like a stalled syscall.
				return "0", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runBrowser(ctx, fs, slave, slave) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("read did not start")
			}
			quitStarted := time.Now()
			if key == "cancel" {
				cancel()
			} else if _, err := master.Write([]byte(key)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil && !(key == "cancel" && errors.Is(err, context.Canceled)) {
					t.Fatalf("exit failed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("exit waited for the blocked filesystem read")
			}
			t.Logf("exit with blocked read: %s", time.Since(quitStarted))
			after, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("terminal settings were not restored: %v", err)
			}
			slave.Close()
			select {
			case data := <-output:
				if !bytes.Contains(data, []byte("\x1b[?1049l")) {
					t.Fatal("alternate screen was not restored")
				}
			case <-time.After(time.Second):
				t.Fatal("terminal output did not close")
			}
			unblock()
		})
	}
}
