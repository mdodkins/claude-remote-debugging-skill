//go:build !linux

package main

import "os/exec"

// tieToBridge has no kernel support here: a bridge that is killed outright
// can leave its ssh behind. A bridge that is asked to stop still takes it.
func tieToBridge(cmd *exec.Cmd) {}
