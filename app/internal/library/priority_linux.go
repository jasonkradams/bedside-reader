package library

import "golang.org/x/sys/unix"

// lowerPriority drops pid to the nicest CPU niceness and the idle I/O class so
// cover extraction never steals cycles or disk bandwidth from playback.
// Best-effort: failures are ignored (extraction still runs, just not niced).
func lowerPriority(pid int) {
	_ = unix.Setpriority(unix.PRIO_PROCESS, pid, 19)
	// ioprio_set(IOPRIO_WHO_PROCESS, pid, IOPRIO_CLASS_IDLE<<IOPRIO_CLASS_SHIFT)
	const ioprioWhoProcess = 1
	const ioprioClassIdle = 3 << 13
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, uintptr(ioprioWhoProcess), uintptr(pid), uintptr(ioprioClassIdle))
}
