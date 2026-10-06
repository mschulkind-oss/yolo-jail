// The libSystem trampolines diskpolicy_darwin.go calls through syscall.syscall: one JMP
// per function, the shape golang.org/x/sys/unix generates for every darwin libc call. The
// syntax is the same on amd64 and arm64, so one file serves both.

#include "textflag.h"

TEXT libc_setiopolicy_np_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_setiopolicy_np(SB)
GLOBL	·libc_setiopolicy_np_trampoline_addr(SB), RODATA, $8
DATA	·libc_setiopolicy_np_trampoline_addr(SB)/8, $libc_setiopolicy_np_trampoline<>(SB)

TEXT libc_getiopolicy_np_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_getiopolicy_np(SB)
GLOBL	·libc_getiopolicy_np_trampoline_addr(SB), RODATA, $8
DATA	·libc_getiopolicy_np_trampoline_addr(SB)/8, $libc_getiopolicy_np_trampoline<>(SB)
