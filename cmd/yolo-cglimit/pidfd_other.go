//go:build !linux

package main

import "errors"

func openPidfd(pid int) (int, error) {
	return -1, errors.New("--join needs pidfd_open, which only Linux has")
}

func closeFd(int) {}
