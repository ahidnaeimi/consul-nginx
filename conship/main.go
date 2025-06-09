package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

var (
	consulAddress     = getEnv("CONSUL_HTTP_ADDR", "http://localhost:8500")
	consulToken       = getEnv("CONSUL_HTTP_TOKEN", "")
	etcdEndpoints     = strings.Split(getEnv("ETCD_ENDPOINTS", "localhost:2379"), ",")
	registeredCache   = make(map[string]ContainerInfo)
	mu                sync.Mutex
	containerName     string = "conship"
	etcdClient        *clientv3.Client
	maxVersionsToKeep = 3
	etcdKeyPrefix     = "conship/"
)

type ContainerInfo struct {
	Ports       []int  `json:"ports"`
	ServiceName string `json:"serviceName"`
	ContainerID string `json:"containerID"`
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

	loadStateFromEtcd()

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
					go handleContainerStart(cli, event.ID)
				case "die", "destroy":
					go handleContainerStop(event.ID)
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
	ctx := context.Background()

	jsonData, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Printf("Failed to inspect container %s: %v", containerID, err)
		return
	}

	// فقط اگر لیبل SERVICE_NAME وجود دارد، ثبت شود
	svcName, ok := jsonData.Config.Labels["SERVICE_NAME"]
	if !ok || svcName == "" {
		log.Printf("SERVICE_NAME label missing for container %s", containerID[:12])
		return
	}

	conshipContainer, err := findContainerByName(cli, ctx, containerName)
	if err != nil {
		log.Printf("Failed to find conship container: %v", err)
		return
	}

	conshipIP := ""
	for _, net := range conshipContainer.NetworkSettings.Networks {
		if net.IPAddress != "" {
			conshipIP = net.IPAddress
			break
		}
	}
	if conshipIP == "" {
		log.Println("Conship container has no IP")
		return
	}

	sharedNet := ""
	for name, net := range jsonData.NetworkSettings.Networks {
		for _, cnet := range conshipContainer.NetworkSettings.Networks {
			if net.NetworkID == cnet.NetworkID {
				sharedNet = name
				break
			}
		}
		if sharedNet != "" {
			break
		}
	}
	if sharedNet == "" {
		log.Printf("Container %s is not on the same network as conship", jsonData.Name)
		return
	}

	containerName := strings.TrimPrefix(jsonData.Name, "/")
	serviceAddress := jsonData.NetworkSettings.Networks[sharedNet].IPAddress

	var ports []int
	var allowedPorts map[int]bool

	portsLabel, ok := jsonData.Config.Labels["SERVICE_PORTS"]
	if ok && portsLabel != "" {
		allowedPorts = make(map[int]bool)
		for _, part := range strings.Split(portsLabel, ",") {
			p, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				log.Printf("Invalid port in SERVICE_PORTS label: %v", err)
				continue
			}
			allowedPorts[p] = true
		}
	}

	for portProto := range jsonData.NetworkSettings.Ports {
		port, err := nat.ParsePort(portProto.Port())
		if err != nil {
			log.Printf("Invalid port %s for container %s: %v", portProto.Port(), containerName, err)
			continue
		}
		if allowedPorts != nil && !allowedPorts[port] {
			continue
		}

		bindings := jsonData.NetworkSettings.Ports[portProto]
		tags := []string{"docker"}
		if len(bindings) > 0 {
			tags = append(tags, "bound")
		} else {
			tags = append(tags, "exposed-only")
		}

		envTags := ""
		for _, env := range jsonData.Config.Env {
			if strings.HasPrefix(env, "SERVICE_TAGS=") {
				envTags = strings.TrimPrefix(env, "SERVICE_TAGS=")
				break
			}
		}
		if envTags != "" {
			for _, tag := range strings.Split(envTags, ",") {
				if trimmed := strings.TrimSpace(tag); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}

		serviceID := fmt.Sprintf("%s-%d", containerID[:12], port)
		serviceDef := map[string]interface{}{
			"ID":      serviceID,
			"Name":    svcName,
			"Address": serviceAddress,
			"Port":    port,
			"Tags":    tags,
			"Check": map[string]interface{}{
				"TCP":                            fmt.Sprintf("%s:%d", serviceAddress, port),
				"Interval":                       "10s",
				"Timeout":                        "1s",
				"DeregisterCriticalServiceAfter": "1m",
			},
		}
		payload, _ := json.Marshal(serviceDef)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/v1/agent/service/register", consulAddress), strings.NewReader(string(payload)))
		if consulToken != "" {
			req.Header.Set("X-Consul-Token", consulToken)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		ports = append(ports, port)
		log.Printf("Service %s on port %d registered", svcName, port)
	}

	// منتظر ماندن تا سرویس healthy شود
	maxRetries := 30 // 30 بار تلاش با فاصله 2 ثانیه = 60 ثانیه
	healthy := false
	for i := 0; i < maxRetries; i++ {
		// چک کردن سلامت سرویس
		url := fmt.Sprintf("%s/v1/agent/health/service/name/%s", consulAddress, svcName)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Printf("Failed to create health check request: %v", err)
			continue
		}
		if consulToken != "" {
			req.Header.Set("X-Consul-Token", consulToken)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Failed to check service health: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Failed to read health check response: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var health []map[string]interface{}
		if err := json.Unmarshal(body, &health); err != nil {
			log.Printf("Failed to parse health check response: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		allHealthy := true
		for _, check := range health {
			if status, ok := check["Status"].(string); ok {
				if status != "passing" {
					allHealthy = false
					break
				}
			}
		}

		if allHealthy {
			healthy = true
			log.Printf("Service %s is now healthy", svcName)
			break
		}

		log.Printf("Waiting for service %s to become healthy... (attempt %d/%d)", svcName, i+1, maxRetries)
		time.Sleep(2 * time.Second)
	}

	if !healthy {
		log.Printf("Service %s did not become healthy after %d seconds", svcName, maxRetries*2)
		return
	}

	// حذف اطلاعات قبلی از etcd
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	etcdKey := fmt.Sprintf("%s%s", etcdKeyPrefix, svcName)
	_, err = etcdClient.Delete(ctx, etcdKey)
	if err != nil {
		log.Printf("Failed to delete old container info from etcd: %v", err)
	} else {
		log.Printf("Successfully deleted old container info from etcd for service %s", svcName)
	}

	// ذخیره اطلاعات جدید در etcd
	containerInfo := ContainerInfo{
		Ports:       ports,
		ServiceName: svcName,
		ContainerID: containerID[:12],
	}
	data, err := json.Marshal(containerInfo)
	if err != nil {
		log.Printf("Failed to marshal container info: %v", err)
		return
	}

	_, err = etcdClient.Put(ctx, etcdKey, string(data))
	if err != nil {
		log.Printf("Failed to save container info to etcd: %v", err)
	} else {
		log.Printf("Successfully saved container info to etcd for service %s", svcName)
	}
}

func handleContainerStop(containerID string) {
	shortID := containerID[:12]

	// mu.Lock()
	// info, ok := registeredContainers[shortID]
	// mu.Unlock()
	mu.Lock()
	info, ok := registeredCache[shortID]
	mu.Unlock()

	if !ok {
		log.Printf("No registered info found for container %s", shortID)
		return
	}

	for _, port := range info.Ports {
		serviceID := fmt.Sprintf("%s-%d", shortID, port)

		// حذف از Agent
		req, err := http.NewRequest("PUT", fmt.Sprintf("%s/v1/agent/service/deregister/%s", consulAddress, serviceID), nil)
		if err != nil {
			log.Printf("Failed to create agent deregister request for %s: %v", serviceID, err)
			continue
		}
		if consulToken != "" {
			req.Header.Set("X-Consul-Token", consulToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Failed to deregister %s from agent: %v", serviceID, err)
		} else {
			resp.Body.Close()
			log.Printf("Deregistered service %s from agent (status %d)", serviceID, resp.StatusCode)
		}

		// حذف از Catalog
		catalogURL := fmt.Sprintf("%s/v1/catalog/service/%s", consulAddress, info.ServiceName)
		catalogReq, err := http.NewRequest("GET", catalogURL, nil)
		if err != nil {
			log.Printf("Failed to create catalog lookup request for %s: %v", serviceID, err)
			continue
		}
		if consulToken != "" {
			catalogReq.Header.Set("X-Consul-Token", consulToken)
		}
		catalogResp, err := http.DefaultClient.Do(catalogReq)
		if err != nil {
			log.Printf("Failed to lookup service %s in catalog: %v", serviceID, err)
			continue
		}

		bodyBytes, _ := io.ReadAll(catalogResp.Body)
		catalogResp.Body.Close()

		if catalogResp.StatusCode != http.StatusOK {
			log.Printf("Service %s not found in catalog (status %d)", serviceID, catalogResp.StatusCode)
			continue
		}

		var catalogEntries []struct {
			Node       string `json:"Node"`
			Datacenter string `json:"Datacenter"`
			ServiceID  string `json:"ServiceID"`
		}
		if err := json.Unmarshal(bodyBytes, &catalogEntries); err != nil {
			log.Printf("Failed to decode catalog response for %s: %v", serviceID, err)
			continue
		}

		for _, entry := range catalogEntries {
			if entry.ServiceID != serviceID {
				continue // فقط سرویسی که مربوط به همین کانتینره حذف شه
			}
			deregisterPayload := map[string]string{
				"Node":       entry.Node,
				"Datacenter": entry.Datacenter,
				"ServiceID":  entry.ServiceID,
			}
			payloadBytes, err := json.Marshal(deregisterPayload)
			if err != nil {
				log.Printf("Failed to marshal catalog deregister payload for %s: %v", serviceID, err)
				continue
			}

			catalogDeregisterReq, err := http.NewRequest("PUT", fmt.Sprintf("%s/v1/catalog/deregister", consulAddress), strings.NewReader(string(payloadBytes)))
			if err != nil {
				log.Printf("Failed to create catalog deregister request for %s: %v", serviceID, err)
				continue
			}
			if consulToken != "" {
				catalogDeregisterReq.Header.Set("X-Consul-Token", consulToken)
			}
			catalogDeregisterReq.Header.Set("Content-Type", "application/json")

			catalogDeregisterResp, err := http.DefaultClient.Do(catalogDeregisterReq)
			if err != nil {
				log.Printf("Failed to deregister %s from catalog: %v", serviceID, err)
				continue
			}
			catalogDeregisterResp.Body.Close()
			log.Printf("Deregistered service %s from catalog (status %d)", serviceID, catalogDeregisterResp.StatusCode)
		}
	}

	mu.Lock()
	delete(registeredCache, shortID)
	mu.Unlock()

	saveStateToEtcd(info.ServiceName)
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

func saveStateToEtcd(serviceName string) {
	mu.Lock()
	defer mu.Unlock()

	data, err := json.Marshal(registeredCache)
	if err != nil {
		log.Printf("Failed to marshal registeredCache: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ذخیره با نسخه جدید
	resp, err := etcdClient.Put(ctx, etcdKeyPrefix+serviceName, string(data))
	if err != nil {
		log.Printf("Failed to put data to etcd: %v", err)
		return
	}

	// گرفتن نسخه‌ها و حذف نسخه‌های قدیمی
	keepRevisions(ctx, serviceName)
	log.Printf("Saved state to etcd with revision %d", resp.Header.Revision)
}

func keepRevisions(ctx context.Context, serviceName string) {
	// گرفتن همه نسخه‌های کلید
	resp, err := etcdClient.Get(ctx, etcdKeyPrefix+serviceName, clientv3.WithPrefix(), clientv3.WithRev(0))
	if err != nil {
		log.Printf("Failed to get revisions from etcd: %v", err)
		return
	}

	// فقط نسخه‌های قدیمی‌تر حذف می‌شوند
	if len(resp.Kvs) <= maxVersionsToKeep {
		return
	}

	// حذف نسخه‌های قدیمی‌تر (اگر کلیدهای مختلف داشتیم)
	// اینجا چون فقط یک کلید هست، نسخه‌های قدیمی با compact پاک می‌شوند
	// etcd خودش نسخه‌ها را مدیریت می‌کند، ولی اینجا برای مثال کد compact می‌آوریم:
	compactRev := resp.Header.Revision - int64(maxVersionsToKeep)
	_, err = etcdClient.Compact(ctx, compactRev)
	if err != nil {
		log.Printf("Failed to compact etcd at revision %d: %v", compactRev, err)
	}
}

func loadStateFromEtcd() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := etcdClient.Get(ctx, etcdKeyPrefix+containerName)
	if err != nil {
		log.Printf("Failed to load state from etcd: %v", err)
		return
	}
	if len(resp.Kvs) == 0 {
		return
	}

	mu.Lock()
	defer mu.Unlock()
	err = json.Unmarshal(resp.Kvs[0].Value, &registeredCache)
	if err != nil {
		log.Printf("Failed to unmarshal registeredCache: %v", err)
	}
}
