package nvidia

const (
	VcudaSocketName    = "vcuda.sock"
	VirtualManagerPath = "/etc/unishare/vm"

	ResourceMemory = "doslab.io/gpu-memory"
	ResourceCore   = "doslab.io/gpu-core"

	AnnAssumeTime   = "doslab.io/gpu-assume-time"
	AnnAssignedFlag = "doslab.io/gpu-assigned"
	//AnnResourceUUID       = "doslab.io/gpu-uuid"
	AnnVCUDAReady = "doslab.io/vcuda"

	ConfigFileName = "vcuda.config"
	pidFileName    = "pids.config"

	CgroupV1Base = "/sys/fs/cgroup/memory"
	CgroupV2Base = "/sys/fs/cgroup"

	CgroupProcs = "cgroup.procs"

	PodQOSGuaranteed = "Guaranteed"
	PodQOSBurstable  = "Burstable"
	PodQOSBestEffort = "BestEffort"

	/** 256MB */
	MemoryBlockSize = 268435456
)

var (
	DriverVersionMajor int
	DriverVersionMinor int
)
