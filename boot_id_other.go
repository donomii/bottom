//go:build !linux && !darwin && !windows

package main

func readBootID() string {
	return ""
}
