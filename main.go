package main

import (
	"encoding/json"
	"fmt"
	"managerGo/nvidia"
	"managerGo/podWatch"
	"net"
	"path"
	"time"

	"github.com/kubesys/client-go/pkg/kubesys"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	url   = "https://133.133.135.134:6443"
	token = "wq-7oZCU9oZwjWHsonFuZOLqYk7tZdUyjjumykOJoq-ZsCj-g"
)

func main() {

	client := kubesys.NewKubernetesClient(url, token)
	client.Init()

	podMgr := podWatch.NewPodManager()
	podWatcher := kubesys.NewKubernetesWatcher(client, podMgr)
	go client.WatchResources("Pod", "", podWatcher)
	nodeByteInfo, _ := client.GetResource("Node", "", "133.133.135.134")

	var nodeInfo v1.Node
	err := json.Unmarshal(nodeByteInfo, &nodeInfo)
	newNodeInfo := nodeInfo.DeepCopy()
	newNodeInfo.Status.Capacity["doslab.io/vmlucore"] = *resource.NewQuantity(int64(99), resource.DecimalSI)
	if err != nil {
		fmt.Println("Unmarshal error")
	} else {
		fmt.Println(newNodeInfo.Status.Capacity)
	}
	delete(newNodeInfo.Status.Capacity, "doslab.io/vmlucore")
	fmt.Println(newNodeInfo.Status.Capacity)

	//client.DeleteResource()
	//client.CreateResource()

	// for {
	// 	resp, err := client.ListResources("Pod", "default")
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	log.Println(1)
	// 	log.Println(string(resp))
	// 	time.Sleep(100 * time.Second)
	// }

}

func registerToKubelet(kubesysClient *kubesys.KubernetesClient) error {
	socketFile := "/var/lib/kubelet/device-plugins/kubelet.sock"
	dialOptions := []grpc.DialOption{grpc.WithInsecure(), grpc.WithDialer(UnixDial), grpc.WithBlock(), grpc.WithTimeout(time.Second * 5)}

	conn, err := grpc.Dial(socketFile, dialOptions...)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	vcoreServer := nvidia.NewVcoreResourceServer("vmlucore.sock", "doslab.io/vmlucore", kubesysClient)
	go vcoreServer.Run()

	req := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(vcoreServer.SocketName()),
		ResourceName: vcoreServer.ResourceName(),
		Options:      &pluginapi.DevicePluginOptions{PreStartRequired: true},
	}

	klog.V(2).Infof("Register to kubelet with endpoint %s", req.Endpoint)
	_, err = client.Register(context.Background(), req)
	if err != nil {
		return err
	}

	return nil
}

func UnixDial(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}
