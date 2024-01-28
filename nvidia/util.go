/**
 * Copyright (2021, ) Institute of Software, Chinese Academy of Sciences
 **/

package nvidia

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

func getArchFamily(computeMajor, computeMinor int) string {
	switch computeMajor {
	case 1:
		return "Tesla"
	case 2:
		return "Fermi"
	case 3:
		return "Kepler"
	case 5:
		return "Maxwell"
	case 6:
		return "Pascal"
	case 7:
		if computeMinor < 5 {
			return "volta"
		}
		return "Turing"
	case 8:
		return "Ampere"
	}
	return "Unknown"
}

func getCgroupVersion() int {
	cmd := exec.Command("sh", "-c", "mount | grep cgroup")
	output, err := cmd.CombinedOutput()
	if err != nil {

	}

	outputStr := string(output)

	// 拆分为行
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.Contains(line, "/sys/fs/cgroup/memory") {
			return 0
		}
	}

	return 1
}

func getCgroupPath(pod *gjson.Result, containerID string) string {
	meta := pod.Get("metadata")
	podUID := meta.Get("uid").String()
	if podUID == "" {
		return ""
	}
	status := pod.Get("status")
	qosClass := status.Get("qosClass").String()
	if qosClass == "" {
		return ""
	}

	podUID = strings.Replace(podUID, "-", "_", -1) + ".slice"
	path := "kubepods.slice"
	switch qosClass {
	case PodQOSGuaranteed:
		path = filepath.Join(path, "kubepods-guaranteed.slice")
		podUID = "kubepods-guaranteed-pod" + podUID
	case PodQOSBurstable:
		path = filepath.Join(path, "kubepods-burstable.slice")
		podUID = "kubepods-burstable-pod" + podUID
	case PodQOSBestEffort:
		path = filepath.Join(path, "kubepods-besteffort.slice")
		podUID = "kubepods-besteffort-pod" + podUID
	}

	path = filepath.Join(path, podUID)
	/*/sys/fs/cgroup/memory/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod13bb153e_b6b5_4ff5_972d_2384da15832f.slice/
	 	docker-2ca438972fbddcba23e1b8d4c5c9c4d28e7a1cbd466067f624434a92e3446ba5.scope
		containerd时对应的是
	*/
	return fmt.Sprintf("%s/docker-%s.scope", path, containerID)
}

func readProcsFile(file string) ([]int, error) {
	f, err := os.Open(file)
	if err != nil {
		log.Errorf("Can't read %s, %s.", file, err)
		return nil, nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	pids := make([]int, 0)
	for scanner.Scan() {
		line := scanner.Text()
		if pid, err := strconv.Atoi(line); err == nil {
			pids = append(pids, pid)
		}
	}

	log.Infof("Read from %s, pids: %v.", file, pids)
	return pids, nil
}
