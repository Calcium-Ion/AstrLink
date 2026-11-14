package core

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObject owns a Job Object that kills its processes when the desktop's
// handle closes, including when the desktop itself dies.
type jobObject windows.Handle

// attachJobObject puts process pid in a new kill-on-close Job Object.
func attachJobObject(pid int) (processTree, error) {
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

// Terminate ends every process in the job. Windows ends them
// asynchronously; Running tells when they are gone.
func (j jobObject) Terminate() error {
	if err := windows.TerminateJobObject(windows.Handle(j), 1); err != nil {
		return fmt.Errorf("TerminateJobObject failed: %w", err)
	}
	return nil
}

// basicAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which
// golang.org/x/sys does not declare.
type basicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// Running is how many processes in the job have not exited yet.
func (j jobObject) Running() (uint32, error) {
	var accounting basicAccounting
	if err := windows.QueryInformationJobObject(
		windows.Handle(j),
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&accounting)),
		uint32(unsafe.Sizeof(accounting)),
		nil,
	); err != nil {
		return 0, fmt.Errorf("QueryInformationJobObject failed: %w", err)
	}
	return accounting.ActiveProcesses, nil
}
