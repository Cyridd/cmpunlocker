// SPDX-License-Identifier: MIT
// Numerical check of independent FP16 WMMA tiles on the selected CUDA device.
#include <cuda_runtime.h>
#include <cuda_fp16.h>
#include <mma.h>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <vector>
#include <string>
#include <algorithm>
#include <cctype>

static void check(cudaError_t status) {
    if (status != cudaSuccess) {
        std::fprintf(stderr, "CUDA failure: %s\n", cudaGetErrorString(status));
        std::exit(1);
    }
}

__global__ void product(const half *a, const half *b, float *c) {
    using namespace nvcuda;
    const int tile = (blockIdx.x * blockDim.x + threadIdx.x) / 32;
    wmma::fragment<wmma::matrix_a, 16, 16, 16, half, wmma::row_major> x;
    wmma::fragment<wmma::matrix_b, 16, 16, 16, half, wmma::col_major> y;
    wmma::fragment<wmma::accumulator, 16, 16, 16, float> z;
    wmma::load_matrix_sync(x, a + tile * 256, 16);
    wmma::load_matrix_sync(y, b + tile * 256, 16);
    wmma::fill_fragment(z, 0.25f);
    for (int repeat = 0; repeat < 4; ++repeat)
        wmma::mma_sync(z, x, y, z);
    wmma::store_matrix_sync(c + tile * 256, z, 16, wmma::mem_row_major);
}

int main(int argc, char** argv) {
    constexpr int tiles = 32, count = tiles * 256;
    std::string requested;
    bool inspect=false;
    for(int i=1;i<argc;i++) {
        std::string arg=argv[i];
        if(arg.rfind("--pci=",0)==0) requested=arg.substr(6);
        else if(arg=="--inspect") inspect=true;
        else { std::fprintf(stderr,"Unknown argument\n"); return 1; }
    }
    if(requested.empty()) { std::fprintf(stderr,"--pci is required\n"); return 1; }
    auto lower=[](std::string s) {
        std::transform(s.begin(),s.end(),s.begin(),[](unsigned char c){return std::tolower(c);}); return s;
    };
    int devices=0, selected=-1;
    check(cudaGetDeviceCount(&devices));
    for(int i=0;i<devices;i++) {
        char candidate[32]{}; check(cudaDeviceGetPCIBusId(candidate,sizeof(candidate),i));
        if(lower(candidate)==lower(requested)) selected=i;
    }
    if(selected<0) { std::fprintf(stderr,"CUDA GPU at requested PCI location not found\n"); return 1; }
    check(cudaSetDevice(selected));
    cudaDeviceProp prop{};
    check(cudaGetDeviceProperties(&prop, selected));
    char bus[32]{};
    check(cudaDeviceGetPCIBusId(bus, sizeof(bus), selected));
    if (prop.major != 7 || prop.minor != 5) {
        std::fprintf(stderr, "Unexpected CUDA device: %s %s sm_%d%d\n",
                     prop.name, bus, prop.major, prop.minor);
        return 1;
    }
    if(inspect) {
        int driver_version=0; check(cudaDriverGetVersion(&driver_version));
        std::printf("{\"cuda_index\":%d,\"sm_major\":%d,\"sm_minor\":%d,\"driver_api_version\":%d,\"device_name\":\"",selected,prop.major,prop.minor,driver_version);
        for(const unsigned char* p=reinterpret_cast<const unsigned char*>(prop.name);*p;p++) {
            if(*p=='"' || *p=='\\') std::putchar('\\');
            if(*p<32) std::printf("\\u%04x",*p); else std::putchar(*p);
        }
        std::puts("\"}");
        return 0;
    }
    std::vector<half> a(count), b(count);
    std::vector<float> out(count);
    for (int tile = 0; tile < tiles; ++tile)
        for (int row = 0; row < 16; ++row)
            for (int col = 0; col < 16; ++col) {
                a[tile * 256 + row * 16 + col] =
                    __float2half(float((tile + 3 * row + col) % 7 - 3));
                b[tile * 256 + col * 16 + row] =
                    __float2half(float((2 * tile + row + 2 * col) % 5 - 2));
            }
    half *da = nullptr, *db = nullptr;
    float *dc = nullptr;
    check(cudaMalloc(&da, count * sizeof(half)));
    check(cudaMalloc(&db, count * sizeof(half)));
    check(cudaMalloc(&dc, count * sizeof(float)));
    check(cudaMemcpy(da, a.data(), count * sizeof(half), cudaMemcpyHostToDevice));
    check(cudaMemcpy(db, b.data(), count * sizeof(half), cudaMemcpyHostToDevice));
    float max_error = 0;
    for (int launch = 0; launch < 20; ++launch) {
        product<<<tiles / 4, 128>>>(da, db, dc);
        check(cudaGetLastError());
        check(cudaMemcpy(out.data(), dc, count * sizeof(float), cudaMemcpyDeviceToHost));
        for (int tile = 0; tile < tiles; ++tile)
            for (int row = 0; row < 16; ++row)
                for (int col = 0; col < 16; ++col) {
                    float expected = 0.25f;
                    for (int k = 0; k < 16; ++k)
                        expected += 4 * __half2float(a[tile * 256 + row * 16 + k]) *
                                        __half2float(b[tile * 256 + col * 16 + k]);
                    const float actual = out[tile * 256 + row * 16 + col];
                    const float error = std::fabs(actual - expected);
                    if (!std::isfinite(actual) || error > 1e-5f) {
                        std::fprintf(stderr, "Mismatch: launch=%d tile=%d row=%d col=%d actual=%g expected=%g\n",
                                     launch, tile, row, col, actual, expected);
                        return 1;
                    }
                    max_error = std::fmax(max_error, error);
                }
    }
    check(cudaFree(dc)); check(cudaFree(db)); check(cudaFree(da));
    std::printf("WMMA_NUMERICAL_PASS: device=%s bus=%s sm_%d%d launches=20 tiles=32 outputs=163840 max_abs_error=%g\n",
                prop.name, bus, prop.major, prop.minor, max_error);
}
