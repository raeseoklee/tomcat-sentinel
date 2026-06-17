//go:build windows

package command

import "os/exec"

func prepareCommand(_ *exec.Cmd) {}

func killCommandGroup(_ *exec.Cmd) {}
