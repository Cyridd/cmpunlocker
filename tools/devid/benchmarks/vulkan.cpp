#include <vulkan/vulkan.h>
#include <algorithm>
#include <cstdint>
#include <cstring>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>
#include <limits>
#include <cmath>
#include <chrono>
#include <set>

#define VK_CHECK(x) do { VkResult r_ = (x); if (r_ != VK_SUCCESS) { std::ostringstream o_; o_ << #x << " failed: " << r_; throw std::runtime_error(o_.str()); } } while (0)

static std::string jsonString(const char* s){
    std::ostringstream o; o<<'"';
    for(const unsigned char* p=reinterpret_cast<const unsigned char*>(s);*p;p++){
        if(*p=='"' || *p=='\\') o<<'\\'<<char(*p);
        else if(*p<32) o<<"\\u"<<std::hex<<std::setfill('0')<<std::setw(4)<<unsigned(*p);
        else o<<char(*p);
    }
    o<<'"'; return o.str();
}

struct Buffer {
    VkDevice d{}; VkBuffer b{}; VkDeviceMemory m{};
    void destroy(){ if(b) vkDestroyBuffer(d,b,nullptr); if(m) vkFreeMemory(d,m,nullptr); b={}; m={}; }
};
struct Image {
    VkDevice d{}; VkImage i{}; VkImageView v{}; VkDeviceMemory m{};
    void destroy(){ if(v) vkDestroyImageView(d,v,nullptr); if(i) vkDestroyImage(d,i,nullptr); if(m) vkFreeMemory(d,m,nullptr); v={}; i={}; m={}; }
};

static uint32_t memType(VkPhysicalDevice pd, uint32_t bits, VkMemoryPropertyFlags want) {
    VkPhysicalDeviceMemoryProperties p{}; vkGetPhysicalDeviceMemoryProperties(pd,&p);
    for(uint32_t i=0;i<p.memoryTypeCount;i++)
        if((bits&(1u<<i)) && (p.memoryTypes[i].propertyFlags&want)==want) return i;
    throw std::runtime_error("no memory type");
}

static Buffer makeBuffer(VkPhysicalDevice pd,VkDevice d,VkDeviceSize size,VkBufferUsageFlags usage,VkMemoryPropertyFlags props){
    Buffer x; x.d=d;
    VkBufferCreateInfo c{}; c.sType=VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO;
    c.size=size; c.usage=usage; c.sharingMode=VK_SHARING_MODE_EXCLUSIVE;
    VK_CHECK(vkCreateBuffer(d,&c,nullptr,&x.b));
    VkMemoryRequirements r{}; vkGetBufferMemoryRequirements(d,x.b,&r);
    VkMemoryAllocateInfo a{}; a.sType=VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO;
    a.allocationSize=r.size; a.memoryTypeIndex=memType(pd,r.memoryTypeBits,props);
    VK_CHECK(vkAllocateMemory(d,&a,nullptr,&x.m));
    VK_CHECK(vkBindBufferMemory(d,x.b,x.m,0));
    return x;
}

static Image makeImage(VkPhysicalDevice pd,VkDevice d,uint32_t w,uint32_t h,VkFormat f,VkImageUsageFlags usage){
    Image x; x.d=d;
    VkImageCreateInfo c{}; c.sType=VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO;
    c.imageType=VK_IMAGE_TYPE_2D; c.format=f; c.extent={w,h,1}; c.mipLevels=1; c.arrayLayers=1;
    c.samples=VK_SAMPLE_COUNT_1_BIT; c.tiling=VK_IMAGE_TILING_OPTIMAL; c.usage=usage;
    c.initialLayout=VK_IMAGE_LAYOUT_UNDEFINED; c.sharingMode=VK_SHARING_MODE_EXCLUSIVE;
    VK_CHECK(vkCreateImage(d,&c,nullptr,&x.i));
    VkMemoryRequirements r{}; vkGetImageMemoryRequirements(d,x.i,&r);
    VkMemoryAllocateInfo a{}; a.sType=VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO;
    a.allocationSize=r.size; a.memoryTypeIndex=memType(pd,r.memoryTypeBits,VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT);
    VK_CHECK(vkAllocateMemory(d,&a,nullptr,&x.m));
    VK_CHECK(vkBindImageMemory(d,x.i,x.m,0));
    VkImageViewCreateInfo v{}; v.sType=VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO;
    v.image=x.i; v.viewType=VK_IMAGE_VIEW_TYPE_2D; v.format=f;
    v.subresourceRange.aspectMask=VK_IMAGE_ASPECT_COLOR_BIT; v.subresourceRange.levelCount=1; v.subresourceRange.layerCount=1;
    VK_CHECK(vkCreateImageView(d,&v,nullptr,&x.v));
    return x;
}

static std::vector<uint32_t> readSpv(const std::string&p){
    std::ifstream f(p,std::ios::binary|std::ios::ate); if(!f) throw std::runtime_error("cannot open "+p);
    auto n=f.tellg(); if(n<=0 || n%4!=0) throw std::runtime_error("invalid SPIR-V size: "+p);
    f.seekg(0); std::vector<uint32_t>x(size_t(n)/4); f.read((char*)x.data(),n); return x;
}
static VkShaderModule shader(VkDevice d,const std::vector<uint32_t>&x){
    VkShaderModuleCreateInfo c{}; c.sType=VK_STRUCTURE_TYPE_SHADER_MODULE_CREATE_INFO; c.codeSize=x.size()*4; c.pCode=x.data();
    VkShaderModule s{}; VK_CHECK(vkCreateShaderModule(d,&c,nullptr,&s)); return s;
}

struct Ctx {
    VkInstance inst{}; VkPhysicalDevice pd{}; VkDevice dev{}; VkQueue q{}; uint32_t qf=0;
    VkCommandPool cp{}; VkCommandBuffer cb{}; VkFence fence{}; VkQueryPool qp{};
    float ts=1.0f; uint32_t timestampBits=0;
    VkFormat fmt=VK_FORMAT_R8G8B8A8_UNORM; VkPhysicalDeviceProperties props{};
    VkPhysicalDeviceIDProperties id{};
    VkPhysicalDeviceDriverProperties driver{};
    VkPhysicalDevicePCIBusInfoPropertiesEXT pci{};
    bool hasPci=false, multiDraw=false; uint32_t physicalIndex=0, aliases=0;
    void destroy(){
        if(qp) vkDestroyQueryPool(dev,qp,nullptr);
        if(fence) vkDestroyFence(dev,fence,nullptr);
        if(cp) vkDestroyCommandPool(dev,cp,nullptr);
        if(dev) vkDestroyDevice(dev,nullptr);
        if(inst) vkDestroyInstance(inst,nullptr);
        *this={};
    }
};

static std::string uuidString(const uint8_t* bytes){
    std::ostringstream o;
    for(unsigned i=0;i<VK_UUID_SIZE;i++) o<<std::hex<<std::setfill('0')<<std::setw(2)<<unsigned(bytes[i]);
    return o.str();
}
static bool hasExtension(VkPhysicalDevice pd,const char* name){
    uint32_t count=0; VK_CHECK(vkEnumerateDeviceExtensionProperties(pd,nullptr,&count,nullptr));
    std::vector<VkExtensionProperties> exts(count);
    VK_CHECK(vkEnumerateDeviceExtensionProperties(pd,nullptr,&count,exts.data()));
    for(const auto& e:exts) if(std::strcmp(e.extensionName,name)==0) return true;
    return false;
}
static Ctx init(uint32_t expectedId){
    Ctx c;
    VkApplicationInfo ai{}; ai.sType=VK_STRUCTURE_TYPE_APPLICATION_INFO; ai.pApplicationName="vk_gpu_microbench"; ai.apiVersion=VK_API_VERSION_1_3;
    VkInstanceCreateInfo ii{}; ii.sType=VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO; ii.pApplicationInfo=&ai;
    VK_CHECK(vkCreateInstance(&ii,nullptr,&c.inst));
    uint32_t n=0; VK_CHECK(vkEnumeratePhysicalDevices(c.inst,&n,nullptr)); if(!n) throw std::runtime_error("no Vulkan devices");
    std::vector<VkPhysicalDevice> p(n); VK_CHECK(vkEnumeratePhysicalDevices(c.inst,&n,p.data()));
    std::set<std::string> identities;
    for(uint32_t i=0;i<n;i++){
        VkPhysicalDeviceIDProperties id{}; id.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_ID_PROPERTIES;
        VkPhysicalDeviceProperties2 pr{}; pr.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_PROPERTIES_2; pr.pNext=&id;
        vkGetPhysicalDeviceProperties2(p[i],&pr);
        if(pr.properties.vendorID!=0x10de || pr.properties.deviceID!=expectedId) continue;
        identities.insert(uuidString(id.deviceUUID)); ++c.aliases;
        if(!c.pd){ c.pd=p[i]; c.props=pr.properties; c.physicalIndex=i; }
    }
    if(identities.size()>1) throw std::runtime_error("multiple distinct GPUs match the requested vendor/Device ID");
    if(!c.pd) throw std::runtime_error("GPU not found");
    c.hasPci=hasExtension(c.pd,VK_EXT_PCI_BUS_INFO_EXTENSION_NAME);
    c.id.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_ID_PROPERTIES;
    c.driver.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_DRIVER_PROPERTIES;
    c.pci.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_PCI_BUS_INFO_PROPERTIES_EXT;
    VkPhysicalDeviceProperties2 pr{}; pr.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_PROPERTIES_2;
    pr.pNext=&c.id; c.id.pNext=&c.driver; c.driver.pNext=c.hasPci?&c.pci:nullptr;
    vkGetPhysicalDeviceProperties2(c.pd,&pr);
    c.id.pNext=nullptr; c.driver.pNext=nullptr;
    c.ts=c.props.limits.timestampPeriod;
    uint32_t qn=0; vkGetPhysicalDeviceQueueFamilyProperties(c.pd,&qn,nullptr);
    std::vector<VkQueueFamilyProperties> q(qn); vkGetPhysicalDeviceQueueFamilyProperties(c.pd,&qn,q.data());
    c.qf=UINT32_MAX;
    const auto required=VK_QUEUE_GRAPHICS_BIT|VK_QUEUE_COMPUTE_BIT;
    for(uint32_t i=0;i<qn;i++) if((q[i].queueFlags&required)==required && q[i].timestampValidBits){ c.qf=i; break; }
    if(c.qf==UINT32_MAX || !c.props.limits.timestampComputeAndGraphics) throw std::runtime_error("no graphics/compute timestamp queue");
    c.timestampBits=q[c.qf].timestampValidBits;
    float prio=1.0f; VkDeviceQueueCreateInfo qc{}; qc.sType=VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO;
    qc.queueFamilyIndex=c.qf; qc.queueCount=1; qc.pQueuePriorities=&prio;
    VkPhysicalDeviceVulkan13Features f{}; f.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VULKAN_1_3_FEATURES;
    VkPhysicalDeviceFeatures2 available{}; available.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_FEATURES_2; available.pNext=&f;
    vkGetPhysicalDeviceFeatures2(c.pd,&available);
    if(!f.synchronization2 || !f.dynamicRendering) throw std::runtime_error("Vulkan 1.3 rendering/synchronization features unavailable");
    f={}; f.sType=VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VULKAN_1_3_FEATURES; f.synchronization2=VK_TRUE; f.dynamicRendering=VK_TRUE;
    c.multiDraw=available.features.multiDrawIndirect;
    VkPhysicalDeviceFeatures enabled{}; enabled.multiDrawIndirect=c.multiDraw;
    VkDeviceCreateInfo dc{}; dc.sType=VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO; dc.pNext=&f; dc.pEnabledFeatures=&enabled; dc.queueCreateInfoCount=1; dc.pQueueCreateInfos=&qc;
    VK_CHECK(vkCreateDevice(c.pd,&dc,nullptr,&c.dev)); vkGetDeviceQueue(c.dev,c.qf,0,&c.q);
    VkCommandPoolCreateInfo pc{}; pc.sType=VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO; pc.queueFamilyIndex=c.qf; pc.flags=VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT;
    VK_CHECK(vkCreateCommandPool(c.dev,&pc,nullptr,&c.cp));
    VkCommandBufferAllocateInfo ca{}; ca.sType=VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO; ca.commandPool=c.cp; ca.level=VK_COMMAND_BUFFER_LEVEL_PRIMARY; ca.commandBufferCount=1;
    VK_CHECK(vkAllocateCommandBuffers(c.dev,&ca,&c.cb));
    VkFenceCreateInfo fi{}; fi.sType=VK_STRUCTURE_TYPE_FENCE_CREATE_INFO; VK_CHECK(vkCreateFence(c.dev,&fi,nullptr,&c.fence));
    VkQueryPoolCreateInfo qi{}; qi.sType=VK_STRUCTURE_TYPE_QUERY_POOL_CREATE_INFO; qi.queryType=VK_QUERY_TYPE_TIMESTAMP; qi.queryCount=2;
    VK_CHECK(vkCreateQueryPool(c.dev,&qi,nullptr,&c.qp));
    return c;
}

static void imageBarrier(VkCommandBuffer cb,VkImage i,VkImageLayout oldL,VkImageLayout newL,VkPipelineStageFlags2 dstStage,VkAccessFlags2 dstAccess,
                         VkPipelineStageFlags2 srcStage=VK_PIPELINE_STAGE_2_NONE,VkAccessFlags2 srcAccess=VK_ACCESS_2_NONE){
    VkImageMemoryBarrier2 b{}; b.sType=VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER_2;
    b.srcStageMask=srcStage; b.srcAccessMask=srcAccess;
    b.dstStageMask=dstStage; b.dstAccessMask=dstAccess; b.oldLayout=oldL; b.newLayout=newL; b.image=i;
    b.srcQueueFamilyIndex=VK_QUEUE_FAMILY_IGNORED; b.dstQueueFamilyIndex=VK_QUEUE_FAMILY_IGNORED;
    b.subresourceRange.aspectMask=VK_IMAGE_ASPECT_COLOR_BIT; b.subresourceRange.levelCount=1; b.subresourceRange.layerCount=1;
    VkDependencyInfo d{}; d.sType=VK_STRUCTURE_TYPE_DEPENDENCY_INFO; d.imageMemoryBarrierCount=1; d.pImageMemoryBarriers=&b;
    vkCmdPipelineBarrier2(cb,&d);
}

static void beginRender(VkCommandBuffer cb,VkImageView v,uint32_t w,uint32_t h,VkAttachmentLoadOp load){
    VkRenderingAttachmentInfo a{}; a.sType=VK_STRUCTURE_TYPE_RENDERING_ATTACHMENT_INFO;
    a.imageView=v; a.imageLayout=VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL; a.loadOp=load; a.storeOp=VK_ATTACHMENT_STORE_OP_STORE;
    VkRenderingInfo r{}; r.sType=VK_STRUCTURE_TYPE_RENDERING_INFO; r.renderArea={{0,0},{w,h}}; r.layerCount=1; r.colorAttachmentCount=1; r.pColorAttachments=&a;
    vkCmdBeginRendering(cb,&r);
}

struct Args { uint32_t deviceId=0,w=640,h=360,iters=2,draws=100,repeats=7,warmup=2;
    std::string shaderDir=".",out,testCase; bool inspect=false; };
static Args args(int ac,char**av){
    Args a; for(int i=1;i<ac;i++){ std::string x=av[i];
        auto v=[&](const char*k)->std::string{return x.rfind(k,0)==0?x.substr(std::strlen(k)):"";};
        if(x.rfind("--device-id=",0)==0)a.deviceId=std::stoul(v("--device-id="),nullptr,16); else if(x.rfind("--shader-dir=",0)==0)a.shaderDir=v("--shader-dir=");
        else if(x.rfind("--width=",0)==0)a.w=std::stoul(v("--width=")); else if(x.rfind("--height=",0)==0)a.h=std::stoul(v("--height="));
        else if(x.rfind("--iters=",0)==0)a.iters=std::stoul(v("--iters=")); else if(x.rfind("--draws=",0)==0)a.draws=std::stoul(v("--draws="));
        else if(x.rfind("--repeats=",0)==0)a.repeats=std::stoul(v("--repeats=")); else if(x.rfind("--output=",0)==0)a.out=v("--output=");
        else if(x.rfind("--warmup=",0)==0)a.warmup=std::stoul(v("--warmup="));
        else if(x.rfind("--case=",0)==0)a.testCase=v("--case=");
        else if(x=="--inspect")a.inspect=true;
        else throw std::runtime_error("unknown argument: "+x);
    }
    if((a.deviceId!=0x1f0b && a.deviceId!=0x1f4b) || !a.w || !a.h || a.w>2048 || a.h>2048 ||
        !a.draws || a.draws>10000 || a.iters>4096 || !a.repeats || a.repeats>100 || a.warmup>20)
        throw std::runtime_error("invalid or missing Device ID/workload bounds");
    if(!a.inspect && a.out.empty()) throw std::runtime_error("--output is required");
    return a;
}

enum class CaseKind { DrawOnly, BindManyOne, BindDraw, ScopeNoBind, BindEveryK, ComputeOnly, ComputeBindDispatch, Indirect, BindOnly, BindAlt };
struct Case { CaseKind kind; uint32_t k; std::string name; };

static VkPipeline makeGraphicsPipeline(Ctx&c,VkShaderModule vs,VkShaderModule fs,VkPipelineLayout layout){
    VkPipelineShaderStageCreateInfo st[2]{};
    st[0].sType=VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO; st[0].stage=VK_SHADER_STAGE_VERTEX_BIT; st[0].module=vs; st[0].pName="main";
    st[1].sType=VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO; st[1].stage=VK_SHADER_STAGE_FRAGMENT_BIT; st[1].module=fs; st[1].pName="main";
    VkPipelineVertexInputStateCreateInfo vi{}; vi.sType=VK_STRUCTURE_TYPE_PIPELINE_VERTEX_INPUT_STATE_CREATE_INFO;
    VkPipelineInputAssemblyStateCreateInfo ia{}; ia.sType=VK_STRUCTURE_TYPE_PIPELINE_INPUT_ASSEMBLY_STATE_CREATE_INFO; ia.topology=VK_PRIMITIVE_TOPOLOGY_TRIANGLE_LIST;
    VkPipelineViewportStateCreateInfo vp{}; vp.sType=VK_STRUCTURE_TYPE_PIPELINE_VIEWPORT_STATE_CREATE_INFO; vp.viewportCount=1; vp.scissorCount=1;
    VkPipelineRasterizationStateCreateInfo rs{}; rs.sType=VK_STRUCTURE_TYPE_PIPELINE_RASTERIZATION_STATE_CREATE_INFO; rs.polygonMode=VK_POLYGON_MODE_FILL; rs.cullMode=VK_CULL_MODE_NONE; rs.frontFace=VK_FRONT_FACE_COUNTER_CLOCKWISE; rs.lineWidth=1.0f;
    VkPipelineMultisampleStateCreateInfo ms{}; ms.sType=VK_STRUCTURE_TYPE_PIPELINE_MULTISAMPLE_STATE_CREATE_INFO; ms.rasterizationSamples=VK_SAMPLE_COUNT_1_BIT;
    VkPipelineColorBlendAttachmentState ca{}; ca.colorWriteMask=0xF;
    VkPipelineColorBlendStateCreateInfo cb{}; cb.sType=VK_STRUCTURE_TYPE_PIPELINE_COLOR_BLEND_STATE_CREATE_INFO; cb.attachmentCount=1; cb.pAttachments=&ca;
    VkDynamicState ds[2]={VK_DYNAMIC_STATE_VIEWPORT,VK_DYNAMIC_STATE_SCISSOR};
    VkPipelineDynamicStateCreateInfo dy{}; dy.sType=VK_STRUCTURE_TYPE_PIPELINE_DYNAMIC_STATE_CREATE_INFO; dy.dynamicStateCount=2; dy.pDynamicStates=ds;
    VkPipelineRenderingCreateInfo pr{}; pr.sType=VK_STRUCTURE_TYPE_PIPELINE_RENDERING_CREATE_INFO; pr.colorAttachmentCount=1; pr.pColorAttachmentFormats=&c.fmt;
    VkGraphicsPipelineCreateInfo pi{}; pi.sType=VK_STRUCTURE_TYPE_GRAPHICS_PIPELINE_CREATE_INFO; pi.pNext=&pr; pi.stageCount=2; pi.pStages=st;
    pi.pVertexInputState=&vi; pi.pInputAssemblyState=&ia; pi.pViewportState=&vp; pi.pRasterizationState=&rs; pi.pMultisampleState=&ms; pi.pColorBlendState=&cb; pi.pDynamicState=&dy; pi.layout=layout;
    VkPipeline p{}; VK_CHECK(vkCreateGraphicsPipelines(c.dev,VK_NULL_HANDLE,1,&pi,nullptr,&p)); return p;
}

static VkPipeline makeComputePipeline(Ctx&c,VkShaderModule cs,VkPipelineLayout layout){
    VkPipelineShaderStageCreateInfo st{}; st.sType=VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO; st.stage=VK_SHADER_STAGE_COMPUTE_BIT; st.module=cs; st.pName="main";
    VkComputePipelineCreateInfo pi{}; pi.sType=VK_STRUCTURE_TYPE_COMPUTE_PIPELINE_CREATE_INFO; pi.stage=st; pi.layout=layout;
    VkPipeline p{}; VK_CHECK(vkCreateComputePipelines(c.dev,VK_NULL_HANDLE,1,&pi,nullptr,&p)); return p;
}

static void verifyImage(Ctx&c,const Args&a,Buffer&readback){
    void* mapped=nullptr; VK_CHECK(vkMapMemory(c.dev,readback.m,0,VK_WHOLE_SIZE,0,&mapped));
    const auto* bytes=static_cast<const uint8_t*>(mapped);
    unsigned errors=0;
    for(uint32_t py: {0u,a.h/2,a.h-1}) for(uint32_t px: {0u,a.w/2,a.w-1}){
        float x=(float(px)+0.5f)/2048.0f, y=(float(py)+0.5f)/2048.0f;
        for(uint32_t i=0;i<a.iters;i++){
            x=std::fma(x,0.9991f,0.00031f); y=std::fma(y,0.9993f,0.00017f);
        }
        float expected[4]={x,y,x*0.5f+y*0.25f,1.0f};
        for(unsigned k=0;k<4;k++){
            int quantized=int(std::lround(std::clamp(expected[k],0.0f,1.0f)*255.0f));
            int actual=bytes[(size_t(py)*a.w+px)*4+k];
            if(std::abs(actual-quantized)>(k==3?0:1)) ++errors;
        }
    }
    vkUnmapMemory(c.dev,readback.m);
    if(errors) throw std::runtime_error("image readback differs from CPU reference at sampled pixels");
}
static double runCase(Ctx&c,const Args&a,const Case&cs,VkPipeline gfx,VkPipeline comp,VkPipeline compAlt,VkPipelineLayout gfxLayout,VkPipelineLayout compLayout,
                      VkDescriptorSet compSet,Image&target,Image&storage,Buffer&indirect,Buffer&readback,VkViewport vp,VkRect2D sc,std::ostream&csv){
    std::vector<double> samples;
    uint32_t gx=(a.w+7)/8, gy=(a.h+7)/8;
    const bool bindOnly=cs.kind==CaseKind::BindOnly || cs.kind==CaseKind::BindAlt;
    const bool compute=cs.kind==CaseKind::ComputeOnly || cs.kind==CaseKind::ComputeBindDispatch;
    for(uint32_t rep=0;rep<a.warmup+a.repeats;rep++){
        auto recordStart=std::chrono::steady_clock::now();
        VK_CHECK(vkResetCommandBuffer(c.cb,0)); VK_CHECK(vkResetFences(c.dev,1,&c.fence));
        VkCommandBufferBeginInfo bi{}; bi.sType=VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO; VK_CHECK(vkBeginCommandBuffer(c.cb,&bi));
        vkCmdResetQueryPool(c.cb,c.qp,0,2); vkCmdWriteTimestamp2(c.cb,VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT,c.qp,0);

        if(bindOnly){
            for(uint32_t i=0;i<a.draws;i++) vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_COMPUTE,
                cs.kind==CaseKind::BindAlt && (i&1)?compAlt:comp);
        } else if(compute){
            imageBarrier(c.cb,storage.i,VK_IMAGE_LAYOUT_UNDEFINED,VK_IMAGE_LAYOUT_GENERAL,VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT,VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT);
            vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_COMPUTE,comp);
            vkCmdBindDescriptorSets(c.cb,VK_PIPELINE_BIND_POINT_COMPUTE,compLayout,0,1,&compSet,0,nullptr);
            vkCmdPushConstants(c.cb,compLayout,VK_SHADER_STAGE_COMPUTE_BIT,0,4,&a.iters);
            for(uint32_t i=0;i<a.draws;i++){
                if(i) imageBarrier(c.cb,storage.i,VK_IMAGE_LAYOUT_GENERAL,VK_IMAGE_LAYOUT_GENERAL,
                    VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT,VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT,
                    VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT,VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT);
                if(cs.kind==CaseKind::ComputeBindDispatch) vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_COMPUTE,comp);
                vkCmdDispatch(c.cb,gx,gy,1);
            }
        } else {
            imageBarrier(c.cb,target.i,VK_IMAGE_LAYOUT_UNDEFINED,VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL,VK_PIPELINE_STAGE_2_COLOR_ATTACHMENT_OUTPUT_BIT,VK_ACCESS_2_COLOR_ATTACHMENT_WRITE_BIT);
            beginRender(c.cb,target.v,a.w,a.h,VK_ATTACHMENT_LOAD_OP_DONT_CARE);
            vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_GRAPHICS,gfx);
            vkCmdSetViewport(c.cb,0,1,&vp); vkCmdSetScissor(c.cb,0,1,&sc);
            vkCmdPushConstants(c.cb,gfxLayout,VK_SHADER_STAGE_FRAGMENT_BIT,0,4,&a.iters);

            switch(cs.kind){
                case CaseKind::DrawOnly:
                    for(uint32_t i=0;i<a.draws;i++) vkCmdDraw(c.cb,3,1,0,0);
                    break;
                case CaseKind::BindManyOne:
                    for(uint32_t i=0;i<a.draws;i++) vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_GRAPHICS,gfx);
                    vkCmdDraw(c.cb,3,1,0,0);
                    break;
                case CaseKind::BindDraw:
                    for(uint32_t i=0;i<a.draws;i++){ vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_GRAPHICS,gfx); vkCmdDraw(c.cb,3,1,0,0); }
                    break;
                case CaseKind::ScopeNoBind:
                    vkCmdEndRendering(c.cb);
                    for(uint32_t i=0;i<a.draws;i++){
                        beginRender(c.cb,target.v,a.w,a.h,VK_ATTACHMENT_LOAD_OP_DONT_CARE);
                        vkCmdDraw(c.cb,3,1,0,0);
                        vkCmdEndRendering(c.cb);
                    }
                    break;
                case CaseKind::BindEveryK:
                    for(uint32_t i=0;i<a.draws;i++){
                        if(i%cs.k==0) vkCmdBindPipeline(c.cb,VK_PIPELINE_BIND_POINT_GRAPHICS,gfx);
                        vkCmdDraw(c.cb,3,1,0,0);
                    }
                    break;
                case CaseKind::Indirect:
                    vkCmdDrawIndirect(c.cb,indirect.b,0,a.draws,sizeof(VkDrawIndirectCommand));
                    break;
                default: break;
            }
            if(cs.kind!=CaseKind::ScopeNoBind) vkCmdEndRendering(c.cb);
        }

        vkCmdWriteTimestamp2(c.cb,VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT,c.qp,1);
        if(!bindOnly){
            Image& result=compute?storage:target;
            imageBarrier(c.cb,result.i,compute?VK_IMAGE_LAYOUT_GENERAL:VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL,
                VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,VK_PIPELINE_STAGE_2_TRANSFER_BIT,VK_ACCESS_2_TRANSFER_READ_BIT,
                compute?VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT:VK_PIPELINE_STAGE_2_COLOR_ATTACHMENT_OUTPUT_BIT,
                compute?VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT:VK_ACCESS_2_COLOR_ATTACHMENT_WRITE_BIT);
            VkBufferImageCopy copy{}; copy.imageSubresource={VK_IMAGE_ASPECT_COLOR_BIT,0,0,1}; copy.imageExtent={a.w,a.h,1};
            vkCmdCopyImageToBuffer(c.cb,result.i,VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,readback.b,1,&copy);
            VkMemoryBarrier2 host{}; host.sType=VK_STRUCTURE_TYPE_MEMORY_BARRIER_2;
            host.srcStageMask=VK_PIPELINE_STAGE_2_TRANSFER_BIT; host.srcAccessMask=VK_ACCESS_2_TRANSFER_WRITE_BIT;
            host.dstStageMask=VK_PIPELINE_STAGE_2_HOST_BIT; host.dstAccessMask=VK_ACCESS_2_HOST_READ_BIT;
            VkDependencyInfo dep{}; dep.sType=VK_STRUCTURE_TYPE_DEPENDENCY_INFO; dep.memoryBarrierCount=1; dep.pMemoryBarriers=&host;
            vkCmdPipelineBarrier2(c.cb,&dep);
        }
        VK_CHECK(vkEndCommandBuffer(c.cb));
        auto submitStart=std::chrono::steady_clock::now();
        VkCommandBufferSubmitInfo si{}; si.sType=VK_STRUCTURE_TYPE_COMMAND_BUFFER_SUBMIT_INFO; si.commandBuffer=c.cb;
        VkSubmitInfo2 sub{}; sub.sType=VK_STRUCTURE_TYPE_SUBMIT_INFO_2; sub.commandBufferInfoCount=1; sub.pCommandBufferInfos=&si;
        VK_CHECK(vkQueueSubmit2(c.q,1,&sub,c.fence)); VK_CHECK(vkWaitForFences(c.dev,1,&c.fence,VK_TRUE,60000000000ull));
        auto completed=std::chrono::steady_clock::now();
        uint64_t q[2]{}; VK_CHECK(vkGetQueryPoolResults(c.dev,c.qp,0,2,sizeof(q),q,sizeof(uint64_t),VK_QUERY_RESULT_64_BIT|VK_QUERY_RESULT_WAIT_BIT));
        uint64_t mask=c.timestampBits==64?UINT64_MAX:((uint64_t(1)<<c.timestampBits)-1);
        double ms=double((q[1]-q[0])&mask)*c.ts/1e6;
        if(!bindOnly) verifyImage(c,a,readback);
        if(rep>=a.warmup){
            samples.push_back(ms);
            csv<<cs.name<<','<<rep-a.warmup<<','<<std::setprecision(12)<<ms<<','
               <<std::chrono::duration<double,std::milli>(submitStart-recordStart).count()<<','
               <<std::chrono::duration<double,std::milli>(completed-submitStart).count()<<','<<(bindOnly?0:9)<<'\n';
        }
    }
    std::sort(samples.begin(),samples.end()); return samples[samples.size()/2];
}

int main(int argc,char**argv){
    try{
        Args a=args(argc,argv); Ctx c=init(a.deviceId);
        if(a.inspect){
            std::cout<<"{\"device_id\": "<<c.props.deviceID<<", \"vendor_id\": "<<c.props.vendorID
                <<", \"uuid\": \""<<uuidString(c.id.deviceUUID)<<"\", \"physical_index\": "<<c.physicalIndex
                <<", \"aliases\": "<<c.aliases<<", \"driver_id\": "<<c.driver.driverID
                <<", \"driver_version_raw\": "<<c.props.driverVersion
                <<", \"driver_info\": "<<jsonString(c.driver.driverInfo)
                <<", \"device_name\": "<<jsonString(c.props.deviceName)<<", \"pci\": \"";
            if(c.hasPci) std::cout<<std::hex<<std::setfill('0')<<std::setw(4)<<c.pci.pciDomain<<':'
                <<std::setw(2)<<c.pci.pciBus<<':'<<std::setw(2)<<c.pci.pciDevice<<'.'<<c.pci.pciFunction;
            std::cout<<"\"}\n"; c.destroy(); return 0;
        }
        std::cout<<"GPU: "<<c.props.deviceName<<"\nAPI: "<<VK_VERSION_MAJOR(c.props.apiVersion)<<'.'<<VK_VERSION_MINOR(c.props.apiVersion)<<'.'<<VK_VERSION_PATCH(c.props.apiVersion)
                 <<"\ntimestampPeriod: "<<c.ts<<" ns/tick\nresolution: "<<a.w<<'x'<<a.h<<" draws: "<<a.draws<<" iters/draw: "<<a.iters<<" repeats: "<<a.repeats<<"\n\n";

        auto vs=shader(c.dev,readSpv(a.shaderDir+"/fullscreen.vert.spv"));
        auto fs=shader(c.dev,readSpv(a.shaderDir+"/workload.frag.spv"));
        auto csmod=shader(c.dev,readSpv(a.shaderDir+"/workload.comp.spv"));

        VkPushConstantRange gfxPC{VK_SHADER_STAGE_FRAGMENT_BIT,0,4};
        VkPipelineLayoutCreateInfo glc{}; glc.sType=VK_STRUCTURE_TYPE_PIPELINE_LAYOUT_CREATE_INFO; glc.pushConstantRangeCount=1; glc.pPushConstantRanges=&gfxPC;
        VkPipelineLayout gfxLayout{}; VK_CHECK(vkCreatePipelineLayout(c.dev,&glc,nullptr,&gfxLayout));
        VkPipeline gfx=makeGraphicsPipeline(c,vs,fs,gfxLayout);

        VkDescriptorSetLayoutBinding sb{0,VK_DESCRIPTOR_TYPE_STORAGE_IMAGE,1,VK_SHADER_STAGE_COMPUTE_BIT, nullptr};
        VkDescriptorSetLayoutCreateInfo slc{}; slc.sType=VK_STRUCTURE_TYPE_DESCRIPTOR_SET_LAYOUT_CREATE_INFO; slc.bindingCount=1; slc.pBindings=&sb;
        VkDescriptorSetLayout sLayout{}; VK_CHECK(vkCreateDescriptorSetLayout(c.dev,&slc,nullptr,&sLayout));
        VkPushConstantRange compPC{VK_SHADER_STAGE_COMPUTE_BIT,0,4};
        VkPipelineLayoutCreateInfo clc{}; clc.sType=VK_STRUCTURE_TYPE_PIPELINE_LAYOUT_CREATE_INFO; clc.setLayoutCount=1; clc.pSetLayouts=&sLayout; clc.pushConstantRangeCount=1; clc.pPushConstantRanges=&compPC;
        VkPipelineLayout compLayout{}; VK_CHECK(vkCreatePipelineLayout(c.dev,&clc,nullptr,&compLayout));
        VkPipeline comp=makeComputePipeline(c,csmod,compLayout);
        VkPipeline compAlt=makeComputePipeline(c,csmod,compLayout);

        Image target=makeImage(c.pd,c.dev,a.w,a.h,c.fmt,VK_IMAGE_USAGE_COLOR_ATTACHMENT_BIT|VK_IMAGE_USAGE_TRANSFER_SRC_BIT);
        Image storage=makeImage(c.pd,c.dev,a.w,a.h,c.fmt,VK_IMAGE_USAGE_STORAGE_BIT|VK_IMAGE_USAGE_TRANSFER_SRC_BIT);
        Buffer indirect=makeBuffer(c.pd,c.dev,VkDeviceSize(a.draws)*sizeof(VkDrawIndirectCommand),VK_BUFFER_USAGE_INDIRECT_BUFFER_BIT,VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT|VK_MEMORY_PROPERTY_HOST_COHERENT_BIT);
        void*mp=nullptr; VK_CHECK(vkMapMemory(c.dev,indirect.m,0,VK_WHOLE_SIZE,0,&mp)); auto*cmds=(VkDrawIndirectCommand*)mp;
        for(uint32_t i=0;i<a.draws;i++) cmds[i]={3,1,0,0};
        vkUnmapMemory(c.dev,indirect.m);
        Buffer readback=makeBuffer(c.pd,c.dev,VkDeviceSize(a.w)*a.h*4,VK_BUFFER_USAGE_TRANSFER_DST_BIT,
            VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT|VK_MEMORY_PROPERTY_HOST_COHERENT_BIT);

        VkDescriptorPoolSize ps{VK_DESCRIPTOR_TYPE_STORAGE_IMAGE,1}; VkDescriptorPoolCreateInfo dp{}; dp.sType=VK_STRUCTURE_TYPE_DESCRIPTOR_POOL_CREATE_INFO; dp.maxSets=1; dp.poolSizeCount=1; dp.pPoolSizes=&ps;
        VkDescriptorPool dpool{}; VK_CHECK(vkCreateDescriptorPool(c.dev,&dp,nullptr,&dpool));
        VkDescriptorSetAllocateInfo da{}; da.sType=VK_STRUCTURE_TYPE_DESCRIPTOR_SET_ALLOCATE_INFO; da.descriptorPool=dpool; da.descriptorSetCount=1; da.pSetLayouts=&sLayout;
        VkDescriptorSet compSet{}; VK_CHECK(vkAllocateDescriptorSets(c.dev,&da,&compSet));
        VkDescriptorImageInfo di{}; di.imageView=storage.v; di.imageLayout=VK_IMAGE_LAYOUT_GENERAL;
        VkWriteDescriptorSet wr{}; wr.sType=VK_STRUCTURE_TYPE_WRITE_DESCRIPTOR_SET; wr.dstSet=compSet; wr.dstBinding=0; wr.descriptorCount=1; wr.descriptorType=VK_DESCRIPTOR_TYPE_STORAGE_IMAGE; wr.pImageInfo=&di;
        vkUpdateDescriptorSets(c.dev,1,&wr,0,nullptr);

        VkViewport vp{0,0,float(a.w),float(a.h),0,1}; VkRect2D sc{{0,0},{a.w,a.h}};
        std::vector<Case> cases={
            {CaseKind::BindOnly,1,"compute_bind_only_same"},
            {CaseKind::BindAlt,1,"compute_bind_only_alt2"},
            {CaseKind::DrawOnly,1,"draw_only"},
            {CaseKind::BindManyOne,1,"bind_many_then_one_draw"},
            {CaseKind::BindDraw,1,"bind_draw"},
            {CaseKind::ScopeNoBind,1,"scope_draw_no_bind"},
            {CaseKind::BindEveryK,1,"bind_every_1_draws"},
            {CaseKind::BindEveryK,2,"bind_every_2_draws"},
            {CaseKind::BindEveryK,4,"bind_every_4_draws"},
            {CaseKind::BindEveryK,8,"bind_every_8_draws"},
            {CaseKind::BindEveryK,16,"bind_every_16_draws"},
            {CaseKind::BindEveryK,64,"bind_every_64_draws"},
            {CaseKind::Indirect,1,"one_draw_indirect"},
            {CaseKind::ComputeOnly,1,"dispatch_only"},
            {CaseKind::ComputeBindDispatch,1,"bind_compute_dispatch"}
        };

        std::ofstream csv(a.out); if(!csv) throw std::runtime_error("cannot open output CSV");
        csv<<"case,repeat,gpu_ms,record_ms,submit_wait_ms,checked_pixels\n";
        std::cout<<std::left<<std::setw(28)<<"case"<<std::setw(8)<<"k"<<std::setw(10)<<"draws"<<std::setw(16)<<"binds"<<"GPU ms\n";
        bool selected=false;
        for(const auto&cs:cases){
            if(!a.testCase.empty() && cs.name!=a.testCase) continue;
            if(cs.kind==CaseKind::Indirect && (!c.multiDraw || a.draws>c.props.limits.maxDrawIndirectCount))
                throw std::runtime_error("requested indirect count unsupported");
            selected=true;
            double ms=runCase(c,a,cs,gfx,comp,compAlt,gfxLayout,compLayout,compSet,target,storage,indirect,readback,vp,sc,csv);
            uint32_t binds=0;
            if(cs.kind==CaseKind::BindManyOne) binds=a.draws;
            else if(cs.kind==CaseKind::BindDraw) binds=a.draws;
            else if(cs.kind==CaseKind::BindEveryK) binds=(a.draws+cs.k-1)/cs.k;
            else if(cs.kind==CaseKind::ComputeBindDispatch) binds=a.draws;
            else if(cs.kind==CaseKind::BindOnly || cs.kind==CaseKind::BindAlt) binds=a.draws;
            std::cout<<std::left<<std::setw(28)<<cs.name<<std::setw(8)<<cs.k<<std::setw(10)<<a.draws<<std::setw(16)<<binds<<std::fixed<<std::setprecision(4)<<ms<<"\n";
        }
        if(!selected) throw std::runtime_error("unknown case");
        csv.flush(); if(!csv) throw std::runtime_error("CSV write failed");
        vkDestroyPipeline(c.dev,compAlt,nullptr);
        vkDestroyDescriptorPool(c.dev,dpool,nullptr); vkDestroyPipeline(c.dev,comp,nullptr); vkDestroyPipeline(c.dev,gfx,nullptr);
        vkDestroyPipelineLayout(c.dev,compLayout,nullptr); vkDestroyPipelineLayout(c.dev,gfxLayout,nullptr); vkDestroyDescriptorSetLayout(c.dev,sLayout,nullptr);
        target.destroy(); storage.destroy(); indirect.destroy(); readback.destroy(); vkDestroyShaderModule(c.dev,csmod,nullptr); vkDestroyShaderModule(c.dev,fs,nullptr); vkDestroyShaderModule(c.dev,vs,nullptr); c.destroy();
        return 0;
    }catch(const std::exception&e){ std::cerr<<"ERROR: "<<e.what()<<"\n"; return 1; }
}
