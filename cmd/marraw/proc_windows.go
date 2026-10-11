package main

import (
	"log"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lowerPriority runs this process below normal, so the window's process,
// at normal, always gets the processor first.
func lowerPriority() {
	if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS); err != nil {
		log.Printf("backend: priority: %v", err)
	}
}

// childAttrs leaves the child in this console, as it is.
func childAttrs(*exec.Cmd) {}

// backendJob holds the backend process in a job that ends it when this
// process ends, even if this one is killed.
var backendJob windows.Handle

// tieToThisProcess puts p in a job that kills it when this process ends,
// however it ends: the closing pipe covers a clean exit, this a crash.
func tieToThisProcess(p *os.Process) {
	if backendJob == 0 {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			log.Printf("backend: job: %v", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			log.Printf("backend: job: %v", err)
			_ = windows.CloseHandle(job)
			return
		}
		backendJob = job
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		log.Printf("backend: job: %v", err)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(backendJob, h); err != nil {
		log.Printf("backend: job: %v", err)
	}
}
