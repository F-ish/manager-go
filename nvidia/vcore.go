package nvidia

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kubesys/client-go/pkg/kubesys"
	"google.golang.org/grpc"
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
	client       *kubesys.KubernetesClient
	srv          *grpc.Server
	socketFile   string
	resourceName string
}

var _ pluginapi.DevicePluginServer = &VcoreResourceServer{} //检测是否实现了ListAndWatch等接口
var _ ResourceServer = &VcoreResourceServer{}               //检测是否实现了SocketName等接口

func NewVcoreResourceServer(coreSocketName string, resourceName string, client *kubesys.KubernetesClient) ResourceServer {
	socketFile := filepath.Join("/var/lib/kubelet/device-plugins/", coreSocketName)

	return &VcoreResourceServer{
		client:       client,
		srv:          grpc.NewServer(),
		socketFile:   socketFile,
		resourceName: resourceName,
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

	//req := reqs.ContainerRequests[0]
	//req.DevicesIDs

	conAllocResp := &pluginapi.ContainerAllocateResponse{
		Envs:        make(map[string]string),
		Mounts:      make([]*pluginapi.Mount, 0),
		Devices:     make([]*pluginapi.DeviceSpec, 0), //代表/dev下的设备
		Annotations: make(map[string]string),
	}

	conAllocResp.Envs["LD_LIBRARY_PATH"] = "/usr/local/nvidia"
	conAllocResp.Envs["NVIDIA_VISIBLE_DEVICES"] = "GPU-7f99cfd5-a48a-0aa5-0662-7bf02b024796"
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
	conAllocResp.Annotations["doslab.io/assign"] = "true"

	allocResp.ContainerResponses = append(allocResp.ContainerResponses, conAllocResp)

	return allocResp, nil
}

func (vr *VcoreResourceServer) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	klog.V(2).Infof("ListAndWatch request for vcore")

	devs := make([]*pluginapi.Device, 0)
	if vr.resourceName != "xxx" {
		devs = vr.getMluDevice()
	} else {
		devs = vr.getNvidiaDevice()
	}

	s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})

	// We don't send unhealthy state
	for {
		time.Sleep(time.Second)
	}

	klog.V(2).Infof("ListAndWatch %s exit", vr.resourceName)

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

	gpuDevices := &pluginapi.Device{
		ID:     fmt.Sprintf("%s-%d", vr.resourceName, 0),
		Health: pluginapi.Healthy,
	}

	devs = append(devs, gpuDevices)

	return devs
}
