cmake_minimum_required(VERSION 3.20)
project(devid_bench LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
find_package(Vulkan REQUIRED)
find_program(GLSLC glslc REQUIRED)
add_executable(devid_vulkan vulkan.cpp)
target_link_libraries(devid_vulkan PRIVATE Vulkan::Vulkan)
target_compile_options(devid_vulkan PRIVATE -O2 -Wall -Wextra -Wpedantic)
foreach(SHADER fullscreen.vert workload.frag workload.comp)
  add_custom_command(OUTPUT ${CMAKE_CURRENT_BINARY_DIR}/${SHADER}.spv
    COMMAND ${GLSLC} -O ${CMAKE_CURRENT_SOURCE_DIR}/shaders/${SHADER}
      -o ${CMAKE_CURRENT_BINARY_DIR}/${SHADER}.spv
    DEPENDS ${CMAKE_CURRENT_SOURCE_DIR}/shaders/${SHADER} VERBATIM)
  list(APPEND SPV ${CMAKE_CURRENT_BINARY_DIR}/${SHADER}.spv)
endforeach()
add_custom_target(shaders ALL DEPENDS ${SPV})
add_dependencies(devid_vulkan shaders)
