//go:build !windows && !linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// lowerPriority runs this process at a nicer niceness, so the window's
// process always gets the processor first.
func lowerPriority() { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10) }

// childAttrs leaves the child as it is: the closing pipe stops it.
func childAttrs(*exec.Cmd) {}

// tieToThisProcess leaves it to the closing pipe.
func tieToThisProcess(*os.Process) {}
