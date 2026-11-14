// SPDX-License-Identifier: MIT
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include <nvos.h>
#include <nv_escape.h>
#include <nv-ioctl-numbers.h>

#if !defined(__linux__) || !defined(__x86_64__)
#error "This observer supports only x86-64 Linux"
#endif

_Static_assert(sizeof(NVOS54_PARAMETERS) == 32, "unexpected RM control ABI");
_Static_assert(sizeof(void *) == 8, "the observer requires a 64-bit process");

/* Process-local observer for the x86-64 Linux RM control ABI. Forward the
 * original ioctl before reading its successful two-byte policy response. */
int ioctl(int fd, unsigned long request, ...)
{
    int (*next_ioctl)(int, unsigned long, ...) = dlsym(RTLD_NEXT, "ioctl");
    va_list args;
    va_start(args, request);
    void *arg = va_arg(args, void *);
    va_end(args);
    if (!next_ioctl) {
        errno = ENOSYS;
        return -1;
    }
    int result = next_ioctl(fd, request, arg);
    int saved_errno = errno;
    if (result == 0 && _IOC_TYPE(request) == NV_IOCTL_MAGIC &&
        _IOC_NR(request) == NV_ESC_RM_CONTROL &&
        _IOC_SIZE(request) == sizeof(NVOS54_PARAMETERS) && arg) {
        const NVOS54_PARAMETERS *control = arg;
        if (control->cmd == 0x20810107U &&
            control->status == 0 && control->paramsSize == 2 && control->params) {
            const uint8_t *response = (const void *)(uintptr_t)control->params;
            char line[96];
            int length = snprintf(line, sizeof(line),
                "RM_POLICY command=20810107 status=00000000 response=%02x %02x\n",
                response[0], response[1]);
            if (length > 0 && (size_t)length < sizeof(line))
                (void)write(STDERR_FILENO, line, (size_t)length);
        }
    }
    errno = saved_errno;
    return result;
}
