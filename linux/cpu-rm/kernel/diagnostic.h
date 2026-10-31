/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef TURING_UNLOCK_DIAGNOSTIC_H
#define TURING_UNLOCK_DIAGNOSTIC_H
#ifdef __KERNEL__
#include <linux/printk.h>
#define GU_NOTE(fmt, ...) pr_info("cmp40_preboot: " fmt, ##__VA_ARGS__)
#define GU_ERROR(fmt, ...) pr_err("cmp40_preboot: " fmt, ##__VA_ARGS__)
#else
#include <stdio.h>
#define GU_NOTE(fmt, ...) fprintf(stderr, "cmp40_preboot: " fmt, ##__VA_ARGS__)
#define GU_ERROR(fmt, ...) fprintf(stderr, "cmp40_preboot: " fmt, ##__VA_ARGS__)
#endif

/* No extra device reads. Preserve the errno and identify the exact guard in
 * the matching source bundled with each kit, including pure parser failures. */
static inline int gu_diagnostic_result(int error, const char *file,
                                      const char *function, unsigned int line)
{
    if (error)
        GU_ERROR("failure %s:%u %s error=%d\n", file, line, function, error);
    return error;
}
#define GU_DIAG(result) gu_diagnostic_result((result), __FILE__, __func__, __LINE__)
#endif
