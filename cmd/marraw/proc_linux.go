package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// lowerPriority runs this process at a nicer niceness, so the window's
// process always gets the processor first. Linux keeps the niceness per
// thread, so each thread running now is set, and the threads made later
// take it from them.
func lowerPriority() {
	ents, err := os.ReadDir("/proc/self/task")
	if err != nil {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
		return
	}
	for _, e := range ents {
		if tid, err := strconv.Atoi(e.Name()); err == nil {
			_ = syscall.Setpriority(syscall.PRIO_PROCESS, tid, 10)
		}
	}
}

// childAttrs has the child stopped when this process dies, however it
// dies.
func childAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

// tieToThisProcess needs nothing more here: childAttrs did it.
func tieToThisProcess(*os.Process) {}
