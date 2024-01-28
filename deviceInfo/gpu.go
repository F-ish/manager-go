package deviceInfo

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/kubesys/client-go/pkg/kubesys"
	v1 "github.com/pttq/kube-gpu/pkg/apis/doslab.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	GPUCRDAPIVersion = "doslab.io/v1"
	GPUCRDNamespace  = "default"
)

type GpuInfo struct {
	count   int
	gpuUid  []string
	memInfo map[string]nvml.Memory //nvml.Memory{total, free ,used}
	client  *kubesys.KubernetesClient
}

func NewGpuInfo(client *kubesys.KubernetesClient) *GpuInfo {
	return &GpuInfo{
		count:   0,
		gpuUid:  make([]string, 0),
		memInfo: make(map[string]nvml.Memory, 0),
		client:  client,
	}
}

func (m *GpuInfo) NvmlTest() {
	var ret nvml.Return

	ret = nvml.Init()
	if ret != nvml.SUCCESS {
		log.Fatalf("Unable to initialize NVML: %v", nvml.ErrorString(ret))
	}
	defer func() {
		ret = nvml.Shutdown()
		if ret != nvml.SUCCESS {
			log.Fatalf("Unable to shutdown NVML: %v", nvml.ErrorString(ret))
		}
	}()

	m.count, ret = nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		log.Fatalf("Unable to get device count: %v", nvml.ErrorString(ret))
	}
	for i := 0; i < m.count; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			log.Fatalf("Unable to get device at index %d: %v", i, nvml.ErrorString(ret))
		}

		uuid, ret := device.GetUUID()
		if ret != nvml.SUCCESS {
			log.Fatalf("Unable to get uuid of device at index %d: %v", i, nvml.ErrorString(ret))
		} else {
			m.gpuUid = append(m.gpuUid, uuid)
		}

		mem, ret := device.GetMemoryInfo()
		if ret != nvml.SUCCESS {
			log.Fatalf("Unable to get mem of device at index %d: %v", i, nvml.ErrorString(ret))
		} else {
			m.memInfo[uuid] = mem
		}

	}
	fmt.Println(m.gpuUid)
	fmt.Println(m.count)
	fmt.Println(m.memInfo["GPU-7f99cfd5-a48a-0aa5-0662-7bf02b024796"].Total)

}

func (m *GpuInfo) GetInfo() (int, []string) {

	return m.count, m.gpuUid
}

func (m *GpuInfo) GetMemInfo() map[string]nvml.Memory {

	return m.memInfo

}

func (m *GpuInfo) CreateCRD(hostname string) {
	for i := 0; i < m.count; i++ {
		gpu := v1.GPU{
			TypeMeta: metav1.TypeMeta{
				Kind:       "GPU",
				APIVersion: GPUCRDAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-gpu-%d", hostname, i),
				Namespace: GPUCRDNamespace,
			},
			Spec: v1.GPUSpec{
				UUID: m.gpuUid[i],
				//Model:  *device.Model,
				//Family: getArchFamily(*device.CudaComputeCapability.Major, *device.CudaComputeCapability.Minor),
				Capacity: v1.R{
					Core:   "100",
					Memory: strconv.Itoa(int(m.memInfo[m.gpuUid[i]].Total)),
				},
				Used: v1.R{
					Core:   "0",
					Memory: "0",
				},
				Node: hostname,
			},
		}

		jb, err := json.Marshal(gpu)
		if err != nil {
			log.Fatalf("Failed to marshal gpu struct, %s.", err)
		}
		_, err = m.client.CreateResource(string(jb))
		if err != nil && err.Error() != "request status 201 Created" {
			log.Fatalf("Failed to create gpu %s, %s.", gpu.Name, err)
		}
	}
}
