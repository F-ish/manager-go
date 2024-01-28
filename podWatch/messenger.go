package podWatch

import (
	"encoding/json"
	"fmt"

	"github.com/kubesys/client-go/pkg/kubesys"
	v1 "k8s.io/api/core/v1"
)

type KubeMessenger struct {
	Client   *kubesys.KubernetesClient
	nodeName string
}

func NewKubeMessenger(Client *kubesys.KubernetesClient, nodeName string) *KubeMessenger {
	return &KubeMessenger{
		Client:   Client,
		nodeName: nodeName,
	}
}

func (m *KubeMessenger) getNodeInfo() *v1.Node {
	node, err := m.Client.GetResource("Node", "", m.nodeName)
	if err != nil {
		fmt.Println("getNodeInfo error")
		return nil
	}
	var nodeInfo v1.Node
	err = json.Unmarshal(node, &nodeInfo)
	if err != nil {
		fmt.Println("Unmarshal error")
		return nil
	}

	return &nodeInfo
}

func (m *KubeMessenger) updateNodeStatus(nodeInfo *v1.Node) error {
	nodeInfoJson, err := json.Marshal(nodeInfo) //??这里要解引用吗
	if err != nil {
		fmt.Println("getNodeInfo error")
		return err
	}
	_, err = m.Client.UpdateResourceStatus(string(nodeInfoJson))
	if err != nil {
		fmt.Println("getNodeInfo error")
		return err
	}
	return nil
}

func (m *KubeMessenger) updateNode(nodeInfo *v1.Node) error {
	nodeInfoJson, err := json.Marshal(nodeInfo)
	if err != nil {
		fmt.Println("getNodeInfo error")
		return err
	}
	_, err = m.Client.UpdateResource(string(nodeInfoJson))
	if err != nil {
		fmt.Println("getNodeInfo error")
		return err
	}
	return nil
}

func (m *KubeMessenger) UpdatePodAnnotations(pod *v1.Pod) error {
	podJson, err := json.Marshal(pod)
	if err != nil {
		return err
	}
	_, err = m.Client.UpdateResource(string(podJson))
	if err != nil {
		return err
	}
	return nil
}

func (m *KubeMessenger) getPodOnNode(nameSpace string, podName string) *v1.Pod {
	podByteInfo, err := m.Client.GetResource("Pod", nameSpace, podName)
	if err != nil {
		fmt.Println("getpodByteInfo error")
		return nil
	}
	var podInfo v1.Pod
	err = json.Unmarshal(podByteInfo, &podInfo)
	if err != nil {
		fmt.Println("Unmarshal error")
		return nil
	}

	return &podInfo
}

func (m *KubeMessenger) GetPendingPodOnNode() []*v1.Pod {
	pendingPodList := make([]*v1.Pod, 0)
	podByteList, err := m.Client.ListResources("Pod", "")
	if err != nil {
		return nil
	}
	var podList v1.PodList
	err = json.Unmarshal(podByteList, &podList)
	if err != nil {
		return nil
	}
	for _, pod := range podList.Items {
		if pod.Spec.NodeName == m.nodeName && pod.Status.Phase == "Pending" {
			podCopy := pod.DeepCopy() //不深拷贝的话podList数据在函数结束就销毁了
			podCopy.APIVersion = "v1"
			podCopy.Kind = "Pod"
			pendingPodList = append(pendingPodList, podCopy)
		}
	}

	return pendingPodList
}
