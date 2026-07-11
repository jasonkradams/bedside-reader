//go:build !linux

package library

// lowerPriority is a no-op on non-Linux platforms (the appliance runs Linux;
// this keeps local dev builds/tests compiling on macOS etc.).
func lowerPriority(pid int) {}
