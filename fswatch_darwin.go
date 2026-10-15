//go:build darwin

package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// watchFiles returns a channel that gets a value soon after a change of 1 of the files. The channel has a buffer of 1.
// It watches each file and its folder with kqueue. The watch of the folder finds a rename over the file, for example an atomic write.
// The watch of the file finds a write in place. After each event, it opens all watches again, because a rename gives a new file.
// A file or a folder that does not exist gets a new try every second. The caller must check the files after each value,
// because a change between 2 watches can give no value. The tick of the caller is the backup for this case.
func watchFiles(ctx context.Context, paths ...string) <-chan struct{} {
	queue, err := unix.Kqueue()
	if err != nil {
		return nil
	}
	changes := make(chan struct{}, 1)
	go func() {
		defer unix.Close(queue)
		watches := map[string]int{}
		closeWatches := func() {
			for target, fd := range watches {
				unix.Close(fd)
				delete(watches, target)
			}
		}
		defer closeWatches()
		// missing holds the paths that did not open. A path that opens later changed, so it gives a value.
		missing := map[string]bool{}
		events := make([]unix.Kevent_t, 8)
		timeout := unix.NsecToTimespec(int64(time.Second))
		for ctx.Err() == nil {
			for _, path := range paths {
				for _, target := range []string{filepath.Dir(path), path} {
					if _, isWatched := watches[target]; isWatched {
						continue
					}
					fd, err := addVnodeWatch(queue, target)
					if err != nil {
						missing[target] = true
						continue
					}
					watches[target] = fd
					if missing[target] {
						delete(missing, target)
						signalWithoutWait(changes)
					}
				}
			}
			count, err := unix.Kevent(queue, nil, events, &timeout)
			if err != nil && !errors.Is(err, unix.EINTR) {
				time.Sleep(time.Second)
				continue
			}
			if count > 0 {
				// A rename or a delete makes a watch useless, so the next pass opens all watches again.
				closeWatches()
				signalWithoutWait(changes)
			}
		}
	}()
	return changes
}

// addVnodeWatch opens the file or the folder only for events and adds a watch for its changes to the queue.
// Close the result to remove the watch.
func addVnodeWatch(queue int, path string) (int, error) {
	fd, err := unix.Open(path, unix.O_EVTONLY, 0)
	if err != nil {
		return 0, err
	}
	var change unix.Kevent_t
	unix.SetKevent(&change, fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_CLEAR|unix.EV_ENABLE)
	change.Fflags = unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_ATTRIB | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE
	if _, err := unix.Kevent(queue, []unix.Kevent_t{change}, nil, nil); err != nil {
		unix.Close(fd)
		return 0, err
	}
	return fd, nil
}
