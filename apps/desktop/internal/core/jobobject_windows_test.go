package core

import (
	"math"
	"strings"
	"testing"
)

func TestJobObjectIsCreatable(t *testing.T) {
	job, err := newJobObject()
	if err != nil {
		t.Fatalf("a configured Job Object should be creatable: %v", err)
	}
	job.Close()
}

func TestJobObjectRejectsAMissingProcess(t *testing.T) {
	_, err := attachJobObject(math.MaxUint32)
	if err == nil {
		t.Fatal("an impossible PID must not be assignable to a Job Object")
	}
	if !strings.Contains(err.Error(), "OpenProcess") && !strings.Contains(err.Error(), "AssignProcessToJobObject") {
		t.Fatalf("unexpected error: %v", err)
	}
}
