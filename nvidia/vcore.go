package nvidia

import (
	"context"
	"fmt"
	"managerGo/deviceInfo"
	"managerGo/podWatch"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	vnvidiaCoreSocketName = "vnvidiacore.sock"
	vmluCoreSocketName    = "vmlucore.sock"
)

type ResourceServer interface {
	SocketName() string
	ResourceName() string
	Stop()
	Run() error
}

type VcoreResourceServer struct {
	kubeMessenger *podWatch.KubeMessenger
	srv           *grpc.Server
	socketFile    string
	resourceName  string
	devInfo       *deviceInfo.GpuInfo
}

var _ pluginapi.DevicePluginServer = &VcoreResourceServer{} //检测是否实现了ListAndWatch等接口
var _ ResourceServer = &VcoreResourceServer{}               //检测是否实现了SocketName等接口

func NewVcoreResourceServer(coreSocketName string, resourceName string, kubeMessenger *podWatch.KubeMessenger, devInfo *deviceInfo.GpuInfo) *VcoreResourceServer {
	socketFile := filepath.Join("/var/lib/kubelet/device-plugins/", coreSocketName)

	return &VcoreResourceServer{
		kubeMessenger: kubeMessenger,
		srv:           grpc.NewServer(),
		socketFile:    socketFile,
		resourceName:  resourceName,
		devInfo:       devInfo,
	}
}

func (vr *VcoreResourceServer) SocketName() string {
	return vr.socketFile
}

func (vr *VcoreResourceServer) ResourceName() string {
	return vr.resourceName
}

func (vr *VcoreResourceServer) Stop() {
	vr.srv.Stop()
}

func (vr *VcoreResourceServer) Run() error {
	pluginapi.RegisterDevicePluginServer(vr.srv, vr)

	err := syscall.Unlink(vr.socketFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	l, err := net.Listen("unix", vr.socketFile)
	if err != nil {
		return err
	}

	klog.V(2).Infof("Server %s is ready at %s", vr.resourceName, vr.socketFile)

	return vr.srv.Serve(l)
}

/** device plugin interface */
func (vr *VcoreResourceServer) Allocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	klog.V(2).Infof("%+v allocation request for vcore", reqs)
	allocResp := &pluginapi.AllocateResponse{}

	req := reqs.ContainerRequests[0]
	allReqDevice := len(req.DevicesIDs)

	/*等待pod对应的virtual server启动（那边通过watch DoModified机制，检测到有gpu需求的pod就创建对应server，那后续已经建好的pod有修改又触发一次？应该在这做状态检查），
	  那也监控pending来创建server不就不用做状态检查了？
	  再进行容器创建（容器没创建pod各字段都是什么？），问题：确定这个allocate请求对应哪个pod,容器没创建pod为pending状态，查询
	  pending状态的pod确定。
	*/

	//获取未创建的gpuPod
	var reqDevCount int
	pendingPod := vr.kubeMessenger.GetPendingPodOnNode()
	var candidatePodList []*v1.Pod
	var flag bool = false
	for _, v := range pendingPod {
		for _, cont := range v.Spec.Containers {
			if _, ok := cont.Resources.Limits[ResourceMemory]; ok {
				flag = true
				break
			}

		}
		if flag {
			candidatePodList = append(candidatePodList, v)
		}
	}
	var confirmPod v1.Pod
	for _, v := range candidatePodList {
		for _, cont := range v.Spec.Containers {
			temp := cont.Resources.Limits[ResourceCore]
			reqDevCount += int(temp.Value())
		}
		if reqDevCount == allReqDevice {
			confirmPod = *v.DeepCopy()
			break
		}
	}
	confirmPod = *candidatePodList[0]
	confirmPod.Annotations[AnnVCUDAReady] = "no"
	// isOk := false
	// for i := 0; i < 100; i++ {
	// 	pod := p.messenger.GetPodOnNode(assumePod.Name, assumePod.Namespace)
	// 	if pod == nil {
	// 		log.Warningf("Failed to get pod %s, on ns %s.", assumePod.Name, assumePod.Namespace)
	// 		time.Sleep(time.Millisecond * 200)
	// 		continue
	// 	}
	// 	ready := getVCUDAReadyFromPodAnnotation(pod)
	// 	if ready != "" {
	// 		isOk = true
	// 		assumePod = pod
	// 		break
	// 	}
	// 	time.Sleep(time.Millisecond * 200)
	// }

	// if !isOk {
	// 	return nil, errors.New("vcuda not ready")
	// }

	// if confirmPod.Annotations[AnnVCUDAReady] == "yes" {
	// 	continue
	// }

	conAllocResp := &pluginapi.ContainerAllocateResponse{
		Envs:        make(map[string]string),
		Mounts:      make([]*pluginapi.Mount, 0),
		Devices:     make([]*pluginapi.DeviceSpec, 0), //代表/dev下的设备,这个设备和pluginapi.Device有什么映射关系吗
		Annotations: make(map[string]string),
	}

	conAllocResp.Envs["LD_LIBRARY_PATH"] = "/usr/local/nvidia"
	conAllocResp.Envs["NVIDIA_VISIBLE_DEVICES"] = "0"
	conAllocResp.Mounts = append(conAllocResp.Mounts, &pluginapi.Mount{
		ContainerPath: "/usr/local/nvidia",
		HostPath:      "/etc/unishare",
		ReadOnly:      true,
	})
	conAllocResp.Devices = append(conAllocResp.Devices, &pluginapi.DeviceSpec{
		ContainerPath: "/dev/nvidiactl",
		HostPath:      "/dev/nvidiactl",
		Permissions:   "rwm",
	})
	conAllocResp.Devices = append(conAllocResp.Devices, &pluginapi.DeviceSpec{
		ContainerPath: "/dev/nvidia-uvm",
		HostPath:      "/dev/nvidia-uvm",
		Permissions:   "rwm",
	})
	conAllocResp.Devices = append(conAllocResp.Devices, &pluginapi.DeviceSpec{
		ContainerPath: "/dev/nvidia-uvm-tools",
		HostPath:      "/dev/nvidia-uvm-tools",
		Permissions:   "rwm",
	})
	conAllocResp.Devices = append(conAllocResp.Devices, &pluginapi.DeviceSpec{
		ContainerPath: "/dev/nvidia0",
		HostPath:      "/dev/nvidia0",
		Permissions:   "rwm",
	})
	conAllocResp.Annotations["doslab.io/assign"] = "true"

	allocResp.ContainerResponses = append(allocResp.ContainerResponses, conAllocResp)

	return allocResp, nil
}

func (vr *VcoreResourceServer) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	klog.V(2).Infof("ListAndWatch request for vcore")

	devs := make([]*pluginapi.Device, 0)
	devs = vr.getNvidiaDevice()

	s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})

	// We don't send unhealthy state
	for {
		time.Sleep(time.Second)
	}

	return nil
}

func (vr *VcoreResourceServer) GetDevicePluginOptions(ctx context.Context, e *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	klog.V(2).Infof("GetDevicePluginOptions request for vcore")
	return &pluginapi.DevicePluginOptions{PreStartRequired: true}, nil
}

func (vr *VcoreResourceServer) PreStartContainer(ctx context.Context, req *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	klog.V(2).Infof("PreStartContainer request for vcore")
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (vr *VcoreResourceServer) GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

func (vr *VcoreResourceServer) getMluDevice() []*pluginapi.Device {
	devs := make([]*pluginapi.Device, 0)

	mluDevices := &pluginapi.Device{
		ID:     fmt.Sprintf("%s-%d", vr.resourceName, 0),
		Health: pluginapi.Healthy,
	}

	devs = append(devs, mluDevices)

	return devs
}

func (vr *VcoreResourceServer) getNvidiaDevice() []*pluginapi.Device {

	devs := make([]*pluginapi.Device, 0)
	n, _ := vr.devInfo.GetInfo()
	for i := int(0); i < n; i++ {
		gpuDevices := &pluginapi.Device{
			ID:     fmt.Sprintf("%s-%d", ResourceCore, i),
			Health: pluginapi.Healthy,
		}
		devs = append(devs, gpuDevices)
	}
	memInfo := vr.devInfo.GetMemInfo()
	var totalMemOnNode uint64
	for _, v := range memInfo {
		totalMemOnNode += v.Total
	}
	memResourceCount := totalMemOnNode / 268435456 //256M = 268435456Bytes
	for i := uint64(0); i < memResourceCount; i++ {
		memDevices := &pluginapi.Device{
			ID:     fmt.Sprintf("%s-%d", ResourceMemory, i),
			Health: pluginapi.Healthy,
		}
		devs = append(devs, memDevices)
	}

	return devs
}
