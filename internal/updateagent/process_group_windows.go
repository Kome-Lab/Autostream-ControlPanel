//go:build windows

package updateagent

import "os/exec"

func configureProcessGroup(*exec.Cmd, bool) func() { return func() {} }
