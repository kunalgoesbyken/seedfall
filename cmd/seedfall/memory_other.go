//go:build !linux && !windows

package main

func freeMemory() (uint64, bool) { return 0, false }
