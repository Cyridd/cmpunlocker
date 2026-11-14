#version 450
layout(location=0) out vec2 uv;
vec2 pos[3] = vec2[](vec2(-1.0,-1.0), vec2(3.0,-1.0), vec2(-1.0,3.0));
void main() {
    gl_Position = vec4(pos[gl_VertexIndex], 0.0, 1.0);
    uv = 0.5 * (pos[gl_VertexIndex] + 1.0);
}
