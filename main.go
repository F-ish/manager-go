package main

import (
	"fmt"
	"log"
	deviceInfo "managerGo/deviceInfo"
	"managerGo/nvidia"
	"managerGo/podWatch"
	"net"
	"os"
	"path"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kubesys/client-go/pkg/kubesys"
	"github.com/spf13/pflag"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/klog"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

var (
	url        = pflag.String("url", "", "https://ip:port")
	token      = pflag.String("token", "", "master node token")
	serverFlag = false
)

func main() {
	pflag.Parse()

	fmt.Println("String Flag:", *url)
	fmt.Println("String Flag:", *token)

	client := kubesys.NewKubernetesClient(*url, *token)
	client.Init()

	podMgr := podWatch.NewPodManager()
	mes := podWatch.NewKubeMessenger(client, "aaa")
	podWatcher := kubesys.NewKubernetesWatcher(client, podMgr)
	go client.WatchResources("Pod", "", podWatcher)
	virtMgr := nvidia.NewVirtualManager(client, podMgr)
	go virtMgr.Run()

	devInfo := deviceInfo.NewGpuInfo(client)
	devInfo.NvmlTest()
	//自己打上环境变量，从环境变量获取节点名称
	devInfo.CreateCRD("133.133.135.73")

	vcoreServer := nvidia.NewVcoreResourceServer("vmlucore.sock", "doslab.io/vmlucore", mes, devInfo)
	go vcoreServer.Run() //错误怎么处理

	//结束处理
	sigChan := nvidia.NewOSWatcher(syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM) //注册信号处理函数，接受到这几个信号将信号通知发送给sigChan
	go func() {
		select {
		case sig := <-sigChan:
			//devicePlugin.Stop()
			vcoreServer.Stop()
			log.Fatalf("Received signal %v, shutting down.", sig)
		}
	}()
	//处理kubelet重启
	watcher, err := nvidia.NewFileWatcher(pluginapi.DevicePluginPath) //监视/var/lib/kubelet/device-plugins下的文件
	if err != nil {
		log.Fatalf("Failed to created file watcher: %s.", err)
	}

restart:

	if serverFlag {
		vcoreServer.Stop()
		if err := os.Remove(pluginapi.DevicePluginPath + "vmlucore.sock"); err != nil && !os.IsNotExist(err) {
			log.Fatalf("Failed to remove sock: %s.", err)
		}
		go vcoreServer.Run()
	}

	err = registerToKubelet(vcoreServer)
	if err != nil {
		serverFlag = false
		goto restart
	}

	for {
		select {
		case event := <-watcher.Events:
			if event.Name == pluginapi.KubeletSocket && event.Op&fsnotify.Create == fsnotify.Create {
				//log.Infof("Inotify: %s created, restarting.", pluginapi.KubeletSocket)
				serverFlag = true
				goto restart
			}
		case err := <-watcher.Errors:
			//log.Warningf("Inotify: %s", err)
			fmt.Println(err)
		}
	}
}

// 多种资源注册怎么安排
func registerToKubelet(vcoreServer *nvidia.VcoreResourceServer) error {
	socketFile := "/var/lib/kubelet/device-plugins/kubelet.sock"
	dialOptions := []grpc.DialOption{grpc.WithInsecure(), grpc.WithDialer(UnixDial), grpc.WithBlock(), grpc.WithTimeout(time.Second * 5)}

	conn, err := grpc.Dial(socketFile, dialOptions...)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)

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
