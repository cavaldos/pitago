//go:build !windows

package app

import (
	"strconv"
	"syscall"
	"testing"
)

func processAlive(t *testing.T, pid string) bool {
	t.Helper()
	n, err := strconv.Atoi(pid)
	if err != nil {
		t.Fatalf("pid %q: %v", pid, err)
	}
	return syscall.Kill(n, 0) == nil
}
