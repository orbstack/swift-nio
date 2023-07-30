package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
	"k8s.io/utils/inotify"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	watcher, err := inotify.NewWatcher()
	check(err)

	watchPath := os.Args[1]
	err = watcher.AddWatch(watchPath, unix.IN_ALL_EVENTS)
	check(err)

	var lastStat unix.Stat_t
	err = unix.Stat(watchPath, &lastStat)
	check(err)

	for {
		select {
		case ev := <-watcher.Event:
			fmt.Printf("[EVENT] Name=%s Mask=%d Cookie=%d\n    ", ev.Name, ev.Mask, ev.Cookie)
			if ev.Mask&unix.IN_ACCESS != 0 {
				fmt.Printf("[access] ")
			}
			if ev.Mask&unix.IN_CREATE != 0 {
				fmt.Printf("[create] ")
			}
			if ev.Mask&unix.IN_DELETE != 0 {
				fmt.Printf("[delete] ")
			}
			if ev.Mask&unix.IN_DELETE_SELF != 0 {
				fmt.Printf("[delete_self] ")
			}
			if ev.Mask&unix.IN_MODIFY != 0 {
				fmt.Printf("[modify] ")
			}
			if ev.Mask&unix.IN_ATTRIB != 0 {
				fmt.Printf("[attrib] ")
			}
			if ev.Mask&unix.IN_CLOSE_WRITE != 0 {
				fmt.Printf("[close_write] ")
			}
			if ev.Mask&unix.IN_CLOSE_NOWRITE != 0 {
				fmt.Printf("[close_nowrite] ")
			}
			if ev.Mask&unix.IN_OPEN != 0 {
				fmt.Printf("[open] ")
			}
			if ev.Mask&unix.IN_MOVED_FROM != 0 {
				fmt.Printf("[moved_from] ")
			}
			if ev.Mask&unix.IN_MOVED_TO != 0 {
				fmt.Printf("[moved_to] ")
			}
			if ev.Mask&unix.IN_MOVE_SELF != 0 {
				fmt.Printf("[move_self] ")
			}
			fmt.Printf("\n")

			// take action
			if ev.Mask&(unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_MOVE_SELF|unix.IN_DELETE_SELF) != 0 {
				// stat
				var newStat unix.Stat_t
				err = unix.Stat(watchPath, &newStat)
				check(err)
				if newStat.Ino == lastStat.Ino {
					fmt.Printf("    same inode: %d\n", lastStat.Ino)
				} else {
					fmt.Printf("    different inode: %d -> %d\n", lastStat.Ino, newStat.Ino)
					//fmt.Printf("    ctime: %d -> %d\n", lastStat.Ctim.Nsec, newStat.Ctim.Nsec)
				}
				lastStat = newStat

				// re-add
				err = watcher.RemoveWatch(watchPath)
				check(err)
				err = watcher.AddWatch(watchPath, unix.IN_ALL_EVENTS)
				check(err)

				// then read the file. stat not enough
				_, err = os.ReadFile(watchPath)
				check(err)
			}
		case err := <-watcher.Error:
			panic(err)
		}
	}
}
