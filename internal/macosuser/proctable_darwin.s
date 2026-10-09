// The libSystem trampoline proctable_darwin.go calls through syscall.syscall6: one JMP, the
// shape golang.org/x/sys/unix generates for every darwin libc call. The syntax is the same on
// amd64 and arm64, so one file serves both.

#include "textflag.h"

TEXT libc_proc_pidinfo_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pidinfo(SB)
GLOBL	·libc_proc_pidinfo_trampoline_addr(SB), RODATA, $8
DATA	·libc_proc_pidinfo_trampoline_addr(SB)/8, $libc_proc_pidinfo_trampoline<>(SB)
