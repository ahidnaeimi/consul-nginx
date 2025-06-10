package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

var (
	consulAddress            = getEnv("CONSUL_HTTP_ADDR", "http://consul:8500")
	consulToken              = getEnv("CONSUL_HTTP_TOKEN", "")
	etcdEndpoints            = strings.Split(getEnv("ETCD_ENDPOINTS", "localhost:2379"), ",")
	containerName     string = "conship"
	etcdClient        *clientv3.Client
	maxVersionsToKeep int64 = 5
)

type ContainerInfo struct {
	Ports       []int  `json:"ports"`
	ServiceName string `json:"serviceName"`
	ContainerID string `json:"containerID"`
}

type ServiceInfo struct {
	SvcName        string       `json:"SvcName"`
	ContainerName  string       `json:"ContainerName"`
	ContainerID    string       `json:"ContainerID"`
	ServiceAddress string       `json:"ServiceAddress"`
	AllowedPorts   map[int]bool `json:"AllowedPorts"`
	Tags           []string     `json:"Tags"`
}

func initContainerName() error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	idPrefix := hostname

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}

	containers, err := cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return err
	}
	etcd := &Etcd{ctx: context.Background()}
	go etcd.watchKeyHistory()

	for _, container := range containers {
		if strings.HasPrefix(container.ID, idPrefix) {
			containerName = strings.TrimPrefix(container.Names[0], "/")
			return nil
		}
	}

	return fmt.Errorf("container not found with ID prefix: %s", idPrefix)
}

func main() {
	var err error
	etcdClient, err = clientv3.New(clientv3.Config{
		Endpoints:   etcdEndpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer etcdClient.Close()

	err = initContainerName()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Container Name:", containerName)

	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Error creating Docker client: %v", err)
	}

	eventsCh, errCh := cli.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(filters.Arg("type", "container")),
	})
	log.Println("Listening for Docker container events...")

	for {
		select {
		case event := <-eventsCh:
			if event.Type == events.ContainerEventType {
				switch event.Action {
				case "start":
					go handleContainerStart(cli, event.Actor.ID)
				case "die":
					log.Printf("Container %s stopped", event.Actor.ID)
					handleContainerStop(cli, event.Actor.ID)
				case "destroy":
					log.Printf("Container %s destroyed", event.Actor.ID)
				}
			}
		case err := <-errCh:
			if err == io.EOF {
				return
			}
			log.Fatalf("Error from Docker events: %v", err)
		}
	}
}

func handleContainerStart(cli *client.Client, containerID string) {

	containerInfo, err := getContainerInfo(cli, containerID)
	if err != nil {
		log.Printf("Failed to get container info with containerID %s: %v", containerID, err)
		return
	}
	for portProto := range containerInfo.AllowedPorts {
		consulRegistered(cli,
			containerInfo.SvcName,
			containerID,
			containerInfo.ServiceAddress,
			portProto,
			containerInfo.Tags)
	}

	// منتظر ماندن تا سرویس healthy شود
	maxRetries := 30 // 30 بار تلاش با فاصله 2 ثانیه = 60 ثانیه
	healthy := false
	for i := 0; i < maxRetries && !healthy; i++ {
		healthy = serviceHealthCheck(containerInfo.SvcName)

		log.Printf("Waiting for service %s to become healthy... (attempt %d/%d)", containerInfo.SvcName, i+1, maxRetries)
		time.Sleep(2 * time.Second)
	}

	if !healthy {
		log.Printf("Service %s did not become healthy after %d seconds", containerInfo.SvcName, maxRetries*2)
		return
	} else {
		log.Printf("Service %s is now healthy", containerInfo.SvcName)
	}
	etcd := &Etcd{ctx: context.Background()}

	etcd.Add(containerInfo)
	prevContainerInfo, err := etcd.GetPrev(containerInfo.SvcName)
	if err != nil {
		log.Println(err)
	} else {
		log.Println("Delete Previous Container")
		removeContainer(cli, prevContainerInfo)
	}
}

func handleContainerStop(cli *client.Client, containerID string) {
	containerInfo, err := getContainerInfo(cli, containerID)
	if err != nil {
		log.Printf("Failed to get container info with containerID %s: %v", containerID, err)
		return
	}
	log.Printf("Die Container Name: %s", containerInfo.ContainerName)
	for port, _ := range containerInfo.AllowedPorts {
		consulDeregistered(containerInfo.SvcName, fmt.Sprintf("%s-%d", containerID[:12], port))
	}
}

func findContainerByName(cli *client.Client, ctx context.Context, name string) (*types.ContainerJSON, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				info, err := cli.ContainerInspect(ctx, c.ID)
				if err != nil {
					return nil, err
				}
				return &info, nil
			}
		}
	}
	return nil, fmt.Errorf("container %s not found", name)
}
