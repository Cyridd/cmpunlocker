#include <algorithm>
#include <array>
#include <cstdint>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

static std::vector<uint8_t> readFile(const std::filesystem::path& path) {
    std::ifstream in(path, std::ios::binary | std::ios::ate);
    if (!in) throw std::runtime_error("cannot open input: " + path.string());
    const auto size = in.tellg();
    if (size <= 0) throw std::runtime_error("empty input: " + path.string());
    in.seekg(0);
    std::vector<uint8_t> data(static_cast<size_t>(size));
    if (!in.read(reinterpret_cast<char*>(data.data()), size))
        throw std::runtime_error("cannot read input: " + path.string());
    return data;
}

static void writeFile(const std::filesystem::path& path,
                      const std::vector<uint8_t>& data) {
    std::ofstream out(path, std::ios::binary | std::ios::trunc);
    if (!out) throw std::runtime_error("cannot open output: " + path.string());
    if (!out.write(reinterpret_cast<const char*>(data.data()), data.size()))
        throw std::runtime_error("cannot write output: " + path.string());
}

static std::vector<size_t> findAll(const std::vector<uint8_t>& data,
                                   const std::array<uint8_t, 8>& pattern) {
    std::vector<size_t> offsets;
    for (auto it = data.begin();;) {
        it = std::search(it, data.end(), pattern.begin(), pattern.end());
        if (it == data.end()) break;
        offsets.push_back(static_cast<size_t>(it - data.begin()));
        ++it;
    }
    return offsets;
}

int main(int argc, char** argv) {
    try {
        if (argc != 4) {
            std::cerr << "usage: patch_glcore INPUT OUTPUT ARGUMENT\n";
            return 2;
        }
        const auto argument = std::stoul(argv[3], nullptr, 0);
        if (argument > UINT32_MAX) throw std::runtime_error("argument exceeds DWORD");

        const std::array<uint8_t, 8> original = {
            0x68, 0x0e, 0x01, 0x20, 0xf0, 0x00, 0x00, 0x00,
        };
        auto replacement = original;
        const uint32_t value = static_cast<uint32_t>(argument);
        for (unsigned i = 0; i < 4; ++i)
            replacement[4 + i] = static_cast<uint8_t>(value >> (i * 8));

        auto data = readFile(argv[1]);
        const auto matches = findAll(data, original);
        if (matches.size() != 2) {
            throw std::runtime_error("expected exactly 2 CALL_MME_MACRO constants, found " +
                                     std::to_string(matches.size()));
        }
        for (const size_t offset : matches)
            std::copy(replacement.begin(), replacement.end(), data.begin() + offset);

        if (!findAll(data, original).empty())
            throw std::runtime_error("original command remains after patch");
        writeFile(argv[2], data);
        std::filesystem::permissions(
            argv[2], std::filesystem::status(argv[1]).permissions(),
            std::filesystem::perm_options::replace);

        std::cout << "patched argument 0xf0 -> 0x" << std::hex << value << std::dec
                  << " at file offsets";
        for (const size_t offset : matches)
            std::cout << " 0x" << std::hex << offset << std::dec;
        std::cout << '\n';
        return 0;
    } catch (const std::exception& e) {
        std::cerr << "ERROR: " << e.what() << '\n';
        return 1;
    }
}
