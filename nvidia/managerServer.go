package nvidia

import (
	"context"
	"encoding/json"
	"errors"
	"managerGo/podWatch"
	"managerGo/vcuda"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/kubesys/client-go/pkg/kubesys"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
)

// #include <stdint.h>
// #include <sys/types.h>
// #include <sys/stat.h>
// #include <fcntl.h>
// #include <string.h>
// #include <sys/file.h>
// #include <time.h>
// #include <stdlib.h>
// #include <unistd.h>
//
// #ifndef NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE
// #define NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE 16
// #endif
//
// #ifndef FILENAME_MAX
// #define FILENAME_MAX 4096
// #endif
//
// struct version_t {
//  int major;
//  int minor;
// } __attribute__((packed, aligned(8)));
//
// struct resource_data_t {
//  char pod_uid[48];
//  int limit;
//  char occupied[4044];
//  char container_name[FILENAME_MAX];
//  char bus_id[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE];
//  uint64_t gpu_memory;
//  int utilization;
//  int hard_limit;
//  struct version_t driver_version;
//  int enable;
// } __attribute__((packed, aligned(8)));
//
// int setting_to_disk(const char* filename, struct resource_data_t* data) {
//  int fd = 0;
//  int wsize = 0;
//  int ret = 0;
//
//  fd = open(filename, O_CREAT | O_TRUNC | O_WRONLY, 00777);
//  if (fd == -1) {
//    return 1;
//  }
//
//  wsize = (int)write(fd, (void*)data, sizeof(struct resource_data_t));
//  if (wsize != sizeof(struct resource_data_t)) {
//    ret = 2;
//	goto DONE;
//  }
//
// DONE:
//  close(fd);
//
//  return ret;
// }
//
// int pids_to_disk(const char* filename, int* data, int size) {
//  int fd = 0;
//  int wsize = 0;
//  struct timespec wait = {
//	.tv_sec = 0, .tv_nsec = 100 * 1000 * 1000,
//  };
//  int ret = 0;
//
//  fd = open(filename, O_CREAT | O_TRUNC | O_WRONLY, 00777);
//  if (fd == -1) {
//    return 1;
//  }
//
//  while (flock(fd, LOCK_EX)) {
//    nanosleep(&wait, NULL);
//  }
//
//  wsize = (int)write(fd, (void*)data, sizeof(int) * size);
//  if (wsize != sizeof(int) * size) {
//	ret = 2;
//    goto DONE;
//  }
//
// DONE:
//  flock(fd, LOCK_UN);
//  close(fd);
//
//  return ret;
// }
import "C"

type VirtualManager struct {
	vcuda.UnimplementedVCUDAServiceServer
	manyServer            map[string]*grpc.Server
	PodByUID              map[string]gjson.Result //包含gpu资源的pod
	ContainerNameByUid    map[string]string
	ContainerUIDByName    map[string]string
	ContainerUIDInPodUID  map[string][]string
	PodVisitedByUID       map[string]bool
	PodDoByUID            map[string]bool
	CoreRequestByPodUID   map[string]int64
	MemoryRequestByPodUID map[string]int64
	client                *kubesys.KubernetesClient
	podMgr                *podWatch.PodManager
	mu                    sync.Mutex
}

func NewVirtualManager(client *kubesys.KubernetesClient, podMgr *podWatch.PodManager) *VirtualManager {
	return &VirtualManager{
		manyServer:            make(map[string]*grpc.Server),
		PodByUID:              make(map[string]gjson.Result),
		ContainerNameByUid:    make(map[string]string, 0),
		ContainerUIDByName:    make(map[string]string, 0),
		ContainerUIDInPodUID:  make(map[string][]string, 0),
		PodVisitedByUID:       make(map[string]bool),
		PodDoByUID:            make(map[string]bool),
		CoreRequestByPodUID:   make(map[string]int64),
		MemoryRequestByPodUID: make(map[string]int64),
		client:                client,
		podMgr:                podMgr,
	}
}

func (vm *VirtualManager) Run() {
	if err := os.MkdirAll(VirtualManagerPath, 0777); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to create %s, %s.", VirtualManagerPath, err)
	}

	for len(vm.podMgr.PodModObj) > 0 {
		if len(vm.podMgr.PodModObj) > 0 {
			jsonObj := vm.podMgr.PodModObj[0]
			vm.podMgr.MuOfModify.Lock()
			vm.podMgr.PodModObj = vm.podMgr.PodModObj[1:]
			vm.podMgr.MuOfModify.Unlock()
			go vm.creatVcudaServer(jsonObj)
		}
		if len(vm.podMgr.PodDelObj) > 0 {
			jsonObj := vm.podMgr.PodDelObj[0]
			vm.podMgr.MuOfDelete.Lock()
			vm.podMgr.PodDelObj = vm.podMgr.PodDelObj[1:]
			vm.podMgr.MuOfDelete.Unlock()
			go vm.deletePodPath(jsonObj)
		}
	}
}

/*
在容器创建前建好，多少个server？
pod传指针还是实体
*/
func (vm *VirtualManager) creatVcudaServer(pod gjson.Result) {
	meta := pod.Get("metadata")
	if !meta.Get("annotations").Exists() {
		return
	}

	annotations := meta.Get("annotations")
	if !annotations.Get(AnnAssumeTime).Exists() {
		return
	}

	flag := annotations.Get(AnnAssignedFlag).String()
	if flag == "" {
		log.Errorln("Failed to get assigned flag.")
		return
	}
	podName := meta.Get("name").String()
	if podName == "" {
		log.Errorln("Failed to get pod name.")
		return
	}
	namespace := meta.Get("namespace").String()
	if namespace == "" {
		log.Errorln("Failed to get pod namespace.")
		return
	}
	podUID := meta.Get("uid").String()
	if podUID == "" {
		log.Errorln("Failed to get pod podUID.")
		return
	}

	vm.mu.Lock()
	if flag == "true" && !vm.PodDoByUID[podUID] {
		vm.mu.Unlock()
		status := pod.Get("status")

		ready := false
		for i := 0; i < 100; i++ {
			if !status.Get("containerStatuses").Exists() {
				log.Errorf("Pod %s on ns %s has no containerStatuses, try later.", podName, namespace)
				time.Sleep(time.Millisecond * 200)
				podByte, err := vm.client.GetResource("Pod", namespace, podName)
				if err != nil {
					log.Errorf("Failed to get pod %s on ns %, %s.", podName, namespace, err)
					return
				}
				pod := kubesys.ToJsonObject(podByte)
				status = pod.Get("status")
			} else {
				ready = true
				break
			}
		}

		if !ready {
			log.Errorf("Pod %s on ns %s has no containerStatuses.", podName, namespace)
			return
		}

		vm.mu.Lock()
		status.Get("containerStatuses").ForEach(func(key, value gjson.Result) bool {
			uidStr := value.Get("containerID").String()
			if uidStr == "" {
				return true
			}
			name := value.Get("name").String()
			if name == "" {
				return true
			}
			uid := strings.Split(uidStr, "docker://")[1]
			vm.ContainerNameByUid[uid] = name
			vm.ContainerUIDByName[name] = uid
			vm.ContainerUIDInPodUID[podUID] = append(vm.ContainerUIDInPodUID[podUID], uid)

			return true
		})

		vm.PodDoByUID[podUID] = true
	}

	if vm.PodVisitedByUID[podUID] {
		vm.mu.Unlock()
		return
	}

	vm.PodVisitedByUID[podUID] = true
	vm.PodByUID[podUID] = pod
	vm.mu.Unlock()

	//creat vcudaServer
	baseDir := filepath.Join(VirtualManagerPath, podUID)
	if err := os.MkdirAll(baseDir, 0777); err != nil && !os.IsExist(err) {
		//log.Errorf("Failed to create %s, %s.", baseDir, err)
		return
	}

	sockfile := filepath.Join(baseDir, VcudaSocketName)
	err := syscall.Unlink(sockfile)
	if err != nil && !os.IsNotExist(err) {
		//log.Errorf("Failed to remove %s, %s.", sockfile, err)
		return
	}

	l, err := net.Listen("unix", sockfile)
	if err != nil {
		//log.Errorf("Failed to listen for %s, %s.", sockfile, err)
		return
	}

	err = os.Chmod(sockfile, 0777)
	if err != nil {
		//log.Errorf("Failed to chmod for %s, %s.", sockfile, err)
		return
	}
	server := grpc.NewServer([]grpc.ServerOption{}...)
	vcuda.RegisterVCUDAServiceServer(server, vm)
	go server.Serve(l)

	vm.mu.Lock()
	vm.manyServer[podUID] = server
	vm.mu.Unlock()
	log.Infof("Success to create VCUDA gRPC server for pod %s.", podUID)

	spec := pod.Get("spec")
	requestMemory, requestCore := int64(0), int64(0)
	spec.Get("containers").ForEach(func(key, value gjson.Result) bool {
		if !(value.Get("resources").Exists()) {
			return true
		}
		resources := value.Get("resources")
		if !(value.Get("limits").Exists()) {
			return true
		}
		limits := resources.Get("limits")
		//必须指定limit的core和memory
		if limits.Get(ResourceCore).Exists() && limits.Get(ResourceMemory).Exists() {
			core := limits.Get(ResourceCore).String()
			mem := limits.Get(ResourceMemory).String()
			m, _ := strconv.ParseInt(core, 10, 64)
			requestCore += m
			m, _ = strconv.ParseInt(mem, 10, 64)
			requestMemory += m
		}

		return true
	})

	vm.mu.Lock()
	vm.CoreRequestByPodUID[podUID] = requestCore
	vm.MemoryRequestByPodUID[podUID] = requestMemory
	vm.mu.Unlock()

	// Update annotation
	time.Sleep(time.Second)
	copyPodBytes, err := vm.client.GetResource("Pod", namespace, podName)
	if err != nil {
		log.Errorf("Failed to get copy pod %s on ns %s, %s.", podName, namespace, err)
		return
	}
	var v1Pod v1.Pod
	json.Unmarshal(copyPodBytes, &v1Pod)
	copyPod := v1Pod.DeepCopy()
	copyPod.Annotations[AnnVCUDAReady] = "yes"
	jsonCopyPod, err := json.Marshal(copyPod)
	_, err = vm.client.UpdateResource(string(jsonCopyPod))
	if err != nil {
		log.Errorf("Failed to set pod %s's annotations, %s.", podName, err)
		return
	}
}

// 删除已经删除GPUpod留下的文件（pid，vcuda，sock）和文件夹
func (vm *VirtualManager) deletePodPath(pod gjson.Result) {
	meta := pod.Get("metadata")
	if !meta.Get("annotations").Exists() {
		return
	}
	annotations := meta.Get("annotations")
	if !annotations.Get(AnnAssumeTime).Exists() {
		return
	}

	podUID := meta.Get("uid").String()

	log.Infof("Clean for pod %s.", podUID)
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if !vm.PodVisitedByUID[podUID] {
		return
	}

	vm.PodVisitedByUID[podUID] = false
	vm.manyServer[podUID].Stop()

	delete(vm.CoreRequestByPodUID, podUID)
	delete(vm.MemoryRequestByPodUID, podUID)
	delete(vm.PodByUID, podUID)
	delete(vm.manyServer, podUID)
	delete(vm.PodDoByUID, podUID)

	for _, uid := range vm.ContainerUIDInPodUID[podUID] {
		name := vm.ContainerNameByUid[uid]
		delete(vm.ContainerNameByUid, uid)
		delete(vm.ContainerUIDByName, name)
	}
	delete(vm.ContainerUIDInPodUID, podUID)

	os.RemoveAll(filepath.Clean(filepath.Join(VirtualManagerPath, podUID)))

}

func (vm *VirtualManager) RegisterVDevice(ctx context.Context, req *vcuda.VDeviceRequest) (*vcuda.VDeviceResponse, error) {

	podUID := req.PodUid
	containerID := req.ContainerId
	containerName := req.ContainerName

	baseDir := ""

	ready := false
	for i := 0; i < 100; i++ {
		vm.mu.Lock()
		if !vm.PodDoByUID[podUID] {
			vm.mu.Unlock()
			time.Sleep(200 * time.Millisecond)
		} else {
			vm.mu.Unlock()
			ready = true
			break
		}
	}

	if !ready {
		return nil, errors.New("no containerStatuses")
	}

	if len(containerName) > 0 {
		log.Infof("Pod %s, container name %s call rpc.", podUID, containerName)
		baseDir = filepath.Join(VirtualManagerPath, podUID, containerName)
		vm.mu.Lock()
		containerID = vm.ContainerUIDByName[containerName]
		vm.mu.Unlock()
	} else {
		log.Infof("Pod %s, container id %s call rpc.", podUID, containerID)
		baseDir = filepath.Join(VirtualManagerPath, podUID, containerID)
		vm.mu.Lock()
		containerName = vm.ContainerNameByUid[containerID]
		vm.mu.Unlock()
	}

	if err := os.MkdirAll(baseDir, 0777); err != nil && !os.IsNotExist(err) {
		log.Errorf("Failed to create %s, %s.", baseDir, err)
		return nil, err
	}

	pidFileName := filepath.Join(baseDir, pidFileName)
	vcudaFileName := filepath.Join(baseDir, ConfigFileName)

	// Create pids.config file
	pod := gjson.Result{}
	vm.mu.Lock()
	pod = vm.PodByUID[podUID]
	vm.mu.Unlock()

	err := vm.createPidsFile(pidFileName, containerID, &pod)
	if err != nil {
		log.Errorf("Failed to create %s, %s.", pidFileName, err)
		return nil, err
	}

	// Create vcuda.config file
	err = vm.createVCUDAFile(vcudaFileName, podUID, containerName)
	if err != nil {
		log.Errorf("Failed to create %s, %s.", vcudaFileName, err)
		return nil, err
	}

	return &vcuda.VDeviceResponse{}, nil
}

func (vm *VirtualManager) createPidsFile(pidFileName, containerID string, pod *gjson.Result) error {
	log.Infof("Write %s", pidFileName)
	cFileName := C.CString(pidFileName)
	defer C.free(unsafe.Pointer(cFileName))

	//cgroupPath, err := getCgroupPath(pod, containerID)
	cgroupPath := getCgroupPath(pod, containerID)
	// if err != nil {
	// 	return err
	// }

	pidsInContainer := make([]int, 0)
	var cgroupBase = " "
	if 1 == getCgroupVersion() {
		cgroupBase = CgroupV2Base
	} else {
		cgroupBase = CgroupV1Base
	}
	baseDir := filepath.Join(cgroupBase, cgroupPath)

	filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if info == nil {
			return nil
		}
		if info.IsDir() || info.Name() != CgroupProcs {
			return nil
		}

		p, err := readProcsFile(path)
		if err == nil {
			pidsInContainer = append(pidsInContainer, p...)
		}

		return nil
	})

	if len(pidsInContainer) == 0 {
		return errors.New("empty pids")
	}

	pids := make([]C.int, len(pidsInContainer))
	for i := range pidsInContainer {
		pids[i] = C.int(pidsInContainer[i])
	}

	if C.pids_to_disk(cFileName, &pids[0], (C.int)(len(pids))) != 0 {
		return errors.New("create pids.config file error")
	}

	return nil
}

func (vm *VirtualManager) createVCUDAFile(vcudaFileName, podUID, containerName string) error {
	log.Infof("Write %s", vcudaFileName)
	requestMemory, requestCore := int64(0), int64(0)
	vm.mu.Lock()
	requestMemory = vm.MemoryRequestByPodUID[podUID]
	requestCore = vm.CoreRequestByPodUID[podUID]
	vm.mu.Unlock()

	var vcudaConfig C.struct_resource_data_t

	cPodUID := C.CString(podUID)
	cContName := C.CString(containerName)
	cFileName := C.CString(vcudaFileName)

	defer C.free(unsafe.Pointer(cPodUID))
	defer C.free(unsafe.Pointer(cContName))
	defer C.free(unsafe.Pointer(cFileName))

	C.strcpy(&vcudaConfig.pod_uid[0], (*C.char)(unsafe.Pointer(cPodUID)))
	C.strcpy(&vcudaConfig.container_name[0], (*C.char)(unsafe.Pointer(cContName)))
	vcudaConfig.gpu_memory = C.uint64_t(requestMemory) * MemoryBlockSize
	vcudaConfig.utilization = C.int(requestCore)
	vcudaConfig.hard_limit = 1
	vcudaConfig.driver_version.major = C.int(DriverVersionMajor)
	vcudaConfig.driver_version.minor = C.int(DriverVersionMinor)
	vcudaConfig.enable = 1

	if C.setting_to_disk(cFileName, &vcudaConfig) != 0 {
		return errors.New("create vcuda.config file error")
	}

	return nil

}
