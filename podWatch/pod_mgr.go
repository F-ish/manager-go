/**
 * Copyright (2021, ) Institute of Software, Chinese Academy of Sciences
 **/

package podWatch

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/kubesys/client-go/pkg/kubesys"
	"github.com/tidwall/gjson"
)

type PodManager struct {
	PodModObj  []gjson.Result
	PodDelObj  []gjson.Result
	MuOfModify sync.Mutex
	MuOfDelete sync.Mutex
}

func NewPodManager() *PodManager {
	return &PodManager{
		PodModObj: make([]gjson.Result, 0),
		PodDelObj: make([]gjson.Result, 0),
	}
}

func (podMgr *PodManager) DoAdded(obj map[string]interface{}) {
	fmt.Println("add pod")
	bytes, _ := json.Marshal(obj)
	fmt.Println(kubesys.ToJsonObject(bytes))
	for k, _ := range obj {
		fmt.Println(k)
	}
}

func (podMgr *PodManager) DoModified(obj map[string]interface{}) {
	bytes, _ := json.Marshal(obj)
	podMgr.MuOfModify.Lock()
	podMgr.PodModObj = append(podMgr.PodModObj, kubesys.ToJsonObject(bytes))
	podMgr.MuOfModify.Unlock()
	fmt.Println(("update pod"))
}

func (podMgr *PodManager) DoDeleted(obj map[string]interface{}) {
	bytes, _ := json.Marshal(obj)
	podMgr.MuOfDelete.Lock()
	podMgr.PodDelObj = append(podMgr.PodDelObj, kubesys.ToJsonObject(bytes))
	podMgr.MuOfDelete.Unlock()

	fmt.Println("delete pod")
}
