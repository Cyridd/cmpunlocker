#version 450
layout(push_constant) uniform PC { uint iters; } pc;
layout(location=0) out vec4 outColor;
void main() {
    vec2 p = gl_FragCoord.xy;
    float x = p.x / 2048.0;
    float y = p.y / 2048.0;
    for (uint i = 0u; i < pc.iters; ++i) {
        x = fma(x, 0.9991, 0.00031);
        y = fma(y, 0.9993, 0.00017);
    }
    outColor = vec4(x, y, x * 0.5 + y * 0.25, 1.0);
}
