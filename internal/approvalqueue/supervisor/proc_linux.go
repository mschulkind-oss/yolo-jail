package supervisor

import "syscall"

// platformAttr makes the kernel kill the child if the queue dies without cleaning up.
func platformAttr(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
