package core

import (
	"fmt"
	"io"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObject owns a Job Object that kills its processes when the desktop's
// handle closes, including when the desktop itself dies.
type jobObject windows.Handle

// attachJobObject puts process pid in a new kill-on-close Job Object.
func attachJobObject(pid int) (io.Closer, error) {
	job, err := newJobObject()
	if err != nil {
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		job.Close()
		return nil, fmt.Errorf("OpenProcess failed: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(windows.Handle(job), process); err != nil {
		job.Close()
		return nil, fmt.Errorf("AssignProcessToJobObject failed: %w", err)
	}
	return job, nil
}

func newJobObject() (jobObject, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObjectW failed: %w", err)
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(handle)
		return 0, fmt.Errorf("SetInformationJobObject failed: %w", err)
	}
	return jobObject(handle), nil
}

func (j jobObject) Close() error { return windows.CloseHandle(windows.Handle(j)) }
