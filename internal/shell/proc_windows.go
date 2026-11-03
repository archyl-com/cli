package shell

import "os/exec"

// killTree leaves cancelling to exec, which kills the process; WaitDelay
// bounds the wait on children that keep its output open.
func killTree(*exec.Cmd) {}
