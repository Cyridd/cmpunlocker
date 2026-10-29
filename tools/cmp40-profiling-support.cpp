// Query CUPTI's range-profiling support before/after the CMP SKU mask.
// Build with the installed CUDA/CUPTI headers and libraries; see research notes.
#include <cuda.h>
#include <cupti_profiler_target.h>
#include <cupti_result.h>

#include <cerrno>
#include <climits>
#include <cstdlib>
#include <iostream>

static const char *level(CUpti_Profiler_Support_Level value)
{
    switch (value)
    {
        case CUPTI_PROFILER_CONFIGURATION_UNKNOWN: return "UNKNOWN";
        case CUPTI_PROFILER_CONFIGURATION_UNSUPPORTED: return "UNSUPPORTED";
        case CUPTI_PROFILER_CONFIGURATION_DISABLED: return "DISABLED";
        case CUPTI_PROFILER_CONFIGURATION_SUPPORTED: return "SUPPORTED";
        default: return "OTHER";
    }
}

static bool cudaOk(CUresult result)
{
    if (result == CUDA_SUCCESS) return true;
    const char *name = nullptr;
    cuGetErrorName(result, &name);
    std::cerr << "CUDA: " << (name ? name : "unknown error") << '\n';
    return false;
}

static bool cuptiOk(CUptiResult result)
{
    if (result == CUPTI_SUCCESS) return true;
    const char *name = nullptr;
    cuptiGetResultString(result, &name);
    std::cerr << "CUPTI: " << (name ? name : "unknown error") << '\n';
    return false;
}

int main(int argc, char **argv)
{
    long index = 0;
    if (argc > 2)
    {
        std::cerr << "usage: cmp40-profiling-support [CUDA_DEVICE_INDEX]\n";
        return 1;
    }
    if (argc == 2)
    {
        char *end = nullptr;
        errno = 0;
        index = std::strtol(argv[1], &end, 10);
        if (errno || end == argv[1] || *end || index < 0 || index > INT_MAX)
        {
            std::cerr << "invalid CUDA device index\n";
            return 1;
        }
    }
    CUdevice device;
    char name[256] = {};
    char bus[32] = {};
    if (!cudaOk(cuInit(0)) || !cudaOk(cuDeviceGet(&device, static_cast<int>(index))) ||
        !cudaOk(cuDeviceGetName(name, sizeof(name), device)) ||
        !cudaOk(cuDeviceGetPCIBusId(bus, sizeof(bus), device)))
        return 1;
    std::cout << "GPU: " << name << " (" << bus << ")\n";

    CUpti_Profiler_Initialize_Params init = {};
    init.structSize = CUpti_Profiler_Initialize_Params_STRUCT_SIZE;
    if (!cuptiOk(cuptiProfilerInitialize(&init))) return 1;

    CUpti_Profiler_DeviceSupported_Params params = {};
    params.structSize = CUpti_Profiler_DeviceSupported_Params_STRUCT_SIZE;
    params.cuDevice = device;
    params.api = CUPTI_PROFILER_RANGE_PROFILING;
    const bool success = cuptiOk(cuptiProfilerDeviceSupported(&params));
    if (success)
    {
        std::cout << "range_profiling=" << level(params.isSupported)
                  << " cmp=" << level(params.cmp)
                  << " architecture=" << level(params.architecture)
                  << " sli=" << level(params.sli)
                  << " vGpu=" << level(params.vGpu)
                  << " confidentialCompute=" << level(params.confidentialCompute)
                  << " wsl=" << level(params.wsl)
                  << " sku=" << level(params.sku) << '\n';
    }
    CUpti_Profiler_DeInitialize_Params fini = {};
    fini.structSize = CUpti_Profiler_DeInitialize_Params_STRUCT_SIZE;
    const bool cleanup = cuptiOk(cuptiProfilerDeInitialize(&fini));
    if (!success || !cleanup) return 1;
    return params.isSupported == CUPTI_PROFILER_CONFIGURATION_SUPPORTED ? 0 : 2;
}
