//go:build windows

package app

import "testing"

func processAlive(*testing.T, string) bool { return false }
