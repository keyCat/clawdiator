//go:build !darwin

package main

import "context"

// watchFiles has no watch on this system yet. The nil channel never sends, so the tick of the caller does the work.
func watchFiles(context.Context, ...string) <-chan struct{} {
	return nil
}
