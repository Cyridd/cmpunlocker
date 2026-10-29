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

template <size_t N>
static std::vector<size_t> findAll(const std::vector<uint8_t>& data,
                                   const std::array<uint8_t, N>& pattern) {
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
        const std::string argumentText = argv[3];
        const bool hex = argumentText.size() > 2 && argumentText[0] == '0' &&
                         (argumentText[1] == 'x' || argumentText[1] == 'X');
        const auto digits = argumentText.substr(hex ? 2 : 0);
        if (digits.empty() || digits.find_first_not_of(
                hex ? "0123456789abcdefABCDEF" : "0123456789") != std::string::npos)
            throw std::runtime_error("argument must be an unsigned decimal or 0x hexadecimal DWORD");
        const auto argument = std::stoull(digits, nullptr, hex ? 16 : 10);
        if (argument > UINT32_MAX) throw std::runtime_error("argument exceeds DWORD");

        const uint32_t value = static_cast<uint32_t>(argument);

        auto data = readFile(argv[1]);
        // The byte pattern is for x86-64 NVIDIA shared libraries only.
        if (data.size() < 64 || data[0] != 0x7f || data[1] != 'E' ||
            data[2] != 'L' || data[3] != 'F' || data[4] != 2 ||
            data[5] != 1 || data[6] != 1 || data[16] != 3 || data[17] != 0 ||
            data[18] != 62 || data[19] != 0)
            throw std::runtime_error("input must be a 64-bit little-endian x86-64 ELF shared library");
        if (std::filesystem::exists(argv[2]) &&
            std::filesystem::equivalent(argv[1], argv[2])) {
            throw std::runtime_error(
                "INPUT and OUTPUT are the same file; refusing to overwrite the original library");
        }
        // Same LOOP/PIPE_NOP/WAIT_FOR_IDLE program in 580, 610 and 615.
        const std::array<uint8_t, 48> mmeProgram = {
            0x03,0x00,0x00,0x00, 0x00,0x00,0xc0,0x1c, 0x00,0x5f,0xcc,0xb1,
            0x13,0x01,0x14,0x9d, 0xa2,0x01,0xc0,0x18, 0x00,0x03,0x8c,0xf1,
            0x03,0x00,0x00,0x00, 0x00,0x00,0xc0,0x18, 0x01,0x03,0x8c,0x31,
            0x03,0x00,0x00,0x00, 0x00,0x00,0xc0,0x18, 0x00,0x03,0x8c,0x31,
        };
        if (findAll(data, mmeProgram).empty())
            throw std::runtime_error("known MME delay program not found; unsupported library");

        // 580 calls slot 51; 610/615 call slot 52. Select by bytes, not release.
        std::array<uint8_t, 8> original = {
            0x68, 0x0e, 0x01, 0x20, 0xf0, 0x00, 0x00, 0x00,
        };
        std::vector<size_t> matches;
        unsigned families = 0;
        for (const uint8_t method : {0x66, 0x68}) {
            auto pattern = original;
            pattern[0] = method;
            const auto found = findAll(data, pattern);
            if (!found.empty()) {
                ++families;
                original = pattern;
                matches = found;
            }
        }
        if (families != 1 || matches.size() != 2) {
            throw std::runtime_error(
                "expected exactly 2 CALL_MME_MACRO constants from one known signature, found " +
                std::to_string(matches.size()) + " in " + std::to_string(families) + " families" +
                "; use an original, unmodified x86-64 libnvidia-glcore.so. "
                "This copy is already patched or has an unsupported command signature");
        }
        for (const size_t offset : matches) {
            // movabs IMM64, REG followed by mov REG,(RAX); reject data matches.
            if (offset < 2 || offset + 11 > data.size() ||
                data[offset - 2] != 0x48 || data[offset - 1] < 0xb8 ||
                data[offset - 1] > 0xbf || data[offset + 8] != 0x48 ||
                data[offset + 9] != 0x89 ||
                data[offset + 10] != ((data[offset - 1] - 0xb8) << 3))
                throw std::runtime_error("command constant is not in the known x86-64 emitter instruction sequence");
        }
        auto replacement = original;
        for (unsigned i = 0; i < 4; ++i)
            replacement[4 + i] = static_cast<uint8_t>(value >> (i * 8));
        for (const size_t offset : matches)
            std::copy(replacement.begin(), replacement.end(), data.begin() + offset);

        if (value != 0xf0 && !findAll(data, original).empty())
            throw std::runtime_error("original command remains after patch");
        writeFile(argv[2], data);
        std::filesystem::permissions(
            argv[2], std::filesystem::status(argv[1]).permissions(),
            std::filesystem::perm_options::replace);

        std::cout << "patched MME slot " << (original[0] / 2)
                  << " argument 0xf0 -> 0x" << std::hex << value << std::dec
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
