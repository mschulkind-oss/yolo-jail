//go:build !linux

package lingerprobe

import "time"

// supported is false off Linux: Start answers ErrUnsupported.
const supported = false

type exitWatch struct{}

func watchExitFile(string, string) (*exitWatch, error) { return nil, ErrUnsupported }

func (w *exitWatch) Death() <-chan time.Time { return nil }

func (w *exitWatch) Close() {}
